#!/usr/bin/env python3
"""Name every shipped executable, retain its symbols, and prove the binding.

The document this module writes states which exact executable bytes a release
shipped, which toolchain and build flags produced them, and which symbol files
belong to them.  A symbol file is only accepted when it matches the shipped
executable by identity: the Mach-O UUID, the ELF build ID, the PE CodeView
GUID/age, equal build info, or the same bytes.

The document carries no credentials, configuration, subscription data or
traffic payload; it describes build identity only.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
from typing import Any, Callable, Iterable, Sequence

SCHEMA = 1
KIND = "dobbyvpn-build-identity"
SHA40 = re.compile(r"[0-9a-f]{40}\Z")
SHA256 = re.compile(r"[0-9a-f]{64}\Z")
VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
PLATFORMS = ("macos", "windows", "linux", "android", "ios")

# The diagnostic changes this document identifies separately.  The product
# compiles them in unconditionally, so the identification is the file set plus
# the constants below, never a feature flag.
DIAGNOSTIC_FILES = (
    "core/diagnostics/timeline.go",
    "core/diagnostics/capture.go",
    "core/diagnostics/export.go",
    "core/log/logger.go",
    "core/cmd/dobbyvpn/logs.go",
    "core/buildinfo/info.go",
    "core/ffi/android/logger.go",
    "ui/windows/DobbyVPN.Windows/NativeDiagnostics.cs",
    "ui/apple/shared/diagnostics/DiagnosticFiles.swift",
    "ui/apple/shared/diagnostics/DiagnosticFilesMigration.swift",
    "ui/apple/ios/integration/IOSLifecycleDiagnostics.swift",
)
DIAGNOSTIC_CONSTANTS = {
    "stage_event": "lifecycle.stage",
    "hang_event": "hang.capture",
    "hang_capture_max_bytes": 1 << 20,
    "max_retained_log_files": 4,
    "rotation_threshold_bytes": 150_000_000,
}

DOCUMENT_KEYS = frozenset({
    "schema", "kind", "repository", "workflow_path", "run_id", "run_number",
    "run_attempt", "source_sha", "source_tree", "version", "android_version_code",
    "apple_build_number", "platform", "architecture", "configuration", "package",
    "toolchain", "build_flags", "diagnostics", "diagnostic_variant", "binaries",
    "third_party",
})
BINARY_KEYS = frozenset({
    "role", "file", "sha256", "size", "identity", "symbol_files", "symbol_digests",
    "match",
})
MATCH_KINDS = ("byte-identical", "macho-uuid", "elf-build-id", "pdb-guid", "go-buildinfo")


class SymbolManifestError(ValueError):
    """The symbol set, the shipped executable or the document is not consistent."""


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def sha256_of(path: Path) -> str:
    """Return the lowercase hex SHA-256 of one regular file."""
    path = Path(path)
    if not path.is_file():
        raise SymbolManifestError(f"file is missing: {path}")
    return _sha256(path)


def _run(command: Sequence[str], *, runner: Callable[..., Any] = subprocess.run) -> subprocess.CompletedProcess:
    try:
        return runner(
            list(command), stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            text=True, check=False, timeout=120,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise SymbolManifestError(f"could not run {command[0]}: {error}") from error


def go_executable() -> str | None:
    """Locate the Go toolchain once, so a caller's PATH never changes identity."""
    found = shutil.which("go")
    if found:
        return found
    for base in Path(__file__).resolve().parents:
        candidate = base / "tools" / "go" / "bin" / "go"
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return str(candidate)
    return None


def go_buildinfo(path: Path) -> dict[str, Any] | None:
    """Read ``go version -m`` for one Go binary; ``None`` when it is not Go."""
    path = Path(path)
    if not path.is_file():
        raise SymbolManifestError(f"file is missing: {path}")
    go = go_executable()
    if go is None:
        return None
    result = _run([go, "version", "-m", str(path)])
    output = (result.stdout or "").strip()
    if result.returncode != 0:
        return None
    first = output.splitlines()[0] if output else ""
    if not first.startswith(str(path)):
        return None
    info: dict[str, Any] = {"go_version": "", "path": "", "mod": [], "build": {}}
    go_line = re.search(r":\s*(go\S+)\Z", first)
    if go_line:
        info["go_version"] = go_line.group(1)
    for line in output.splitlines()[1:]:
        stripped = line.strip()
        if stripped.startswith("path\t"):
            info["path"] = stripped[5:].strip()
        elif stripped.startswith("mod\t"):
            info["mod"].append(stripped[4:].strip())
        elif stripped.startswith("build\t"):
            entry = stripped[6:].strip()
            if "=" in entry:
                key, value = entry.split("=", 1)
                info["build"][key] = value.strip("'\"")
            else:
                info["build"][entry] = ""
        elif stripped.startswith("dep\t"):
            info["mod"].append(stripped[4:].strip())
    return info


# The Mach-O magic words. Only a file that carries one is asked of dwarfdump,
# so identification of a PE, ELF or PDB artifact never depends on a macOS tool.
MACHO_MAGICS = frozenset({0xFEEDFACE, 0xCEFAEDFE, 0xFEEDFACF, 0xCFFAEDFE, 0xCAFEBABE, 0xBEBAFECA})


def is_macho(path: Path) -> bool:
    """True when the file's leading bytes are a Mach-O magic word."""
    with Path(path).open("rb") as handle:
        head = handle.read(4)
    return len(head) == 4 and struct.unpack(">I", head)[0] in MACHO_MAGICS


def macho_uuid(path: Path) -> str | None:
    """Read the Mach-O LC_UUID with ``dwarfdump``; ``None`` when not Mach-O.

    A Mach-O without the tool is an error: a release must never ship a Mac
    artifact whose identity could not be read. Non-Mach-O files never invoke it.
    """
    path = Path(path)
    if not path.is_file():
        raise SymbolManifestError(f"file is missing: {path}")
    if not is_macho(path):
        return None
    if shutil.which("dwarfdump") is None:
        raise SymbolManifestError(f"could not run dwarfdump for {path}: tool is not on PATH")
    result = _run(["dwarfdump", "--uuid", str(path)])
    if result.returncode != 0:
        return None
    match = re.search(r"UUID:\s*([0-9A-Fa-f-]{36})", result.stdout or "")
    return match.group(1).upper() if match else None


def _elf_header(data: bytes) -> dict[str, Any] | None:
    """Read the ELF header fields needed for sections and notes."""
    if len(data) < 0x34 or data[:4] != b"\x7fELF":
        return None
    elf_class, elf_data = data[4], data[5]
    if elf_class not in (1, 2) or elf_data not in (1, 2):
        return None
    endian = "<" if elf_data == 1 else ">"
    if elf_class == 2:
        if len(data) < 0x40:
            return None
        shoff, = struct.unpack_from(endian + "Q", data, 0x28)
        shentsize, shnum, shstrndx = struct.unpack_from(endian + "HHH", data, 0x3A)
    else:
        shoff, = struct.unpack_from(endian + "I", data, 0x20)
        shentsize, shnum, shstrndx = struct.unpack_from(endian + "HHH", data, 0x2E)
    return {
        "class": elf_class, "endian": endian, "shoff": shoff,
        "shentsize": shentsize, "shnum": shnum, "shstrndx": shstrndx,
    }


def _elf_sections_raw(data: bytes) -> list[dict[str, Any]]:
    header = _elf_header(data)
    if header is None:
        return []
    endian = header["endian"]
    sections: list[dict[str, Any]] = []
    for index in range(header["shnum"]):
        offset = header["shoff"] + index * header["shentsize"]
        if offset + header["shentsize"] > len(data):
            break
        if header["class"] == 2:
            name, kind = struct.unpack_from(endian + "II", data, offset)
            _flags, _addr, sec_offset, size = struct.unpack_from(endian + "QQQQ", data, offset + 8)
        else:
            name, kind = struct.unpack_from(endian + "II", data, offset)
            _flags, _addr, sec_offset, size = struct.unpack_from(endian + "IIII", data, offset + 8)
        sections.append({"name_offset": name, "type": kind, "offset": sec_offset, "size": size})
    if header["shstrndx"] < len(sections):
        table = sections[header["shstrndx"]]
        names = data[table["offset"]:table["offset"] + table["size"]]
        for section in sections:
            start_at = section["name_offset"]
            end_at = names.find(b"\0", start_at)
            raw = names[start_at:end_at if end_at >= 0 else len(names)]
            section["name"] = raw.decode("ascii", errors="replace")
    return sections


def elf_build_id(path: Path) -> str | None:
    """Read the ELF ``.note.gnu.build-id`` value; ``None`` when absent."""
    path = Path(path)
    if not path.is_file():
        raise SymbolManifestError(f"file is missing: {path}")
    data = path.read_bytes()
    header = _elf_header(data)
    if header is None:
        return None
    endian = header["endian"]
    for section in _elf_sections_raw(data):
        if section["type"] != 7:  # SHT_NOTE
            continue
        cursor = section["offset"]
        limit = min(section["offset"] + section["size"], len(data))
        while cursor + 12 <= limit:
            name_size, desc_size, note_type = struct.unpack_from(endian + "III", data, cursor)
            name_start = cursor + 12
            name = data[name_start:name_start + name_size]
            desc_start = name_start + ((name_size + 3) // 4) * 4
            desc = data[desc_start:desc_start + desc_size]
            cursor = desc_start + ((desc_size + 3) // 4) * 4
            if name.rstrip(b"\0") == b"GNU" and note_type == 3 and desc:
                return desc.hex()
    return None


def elf_sections(path: Path) -> dict[str, int]:
    """Map section name to size from the ELF section table."""
    path = Path(path)
    if not path.is_file():
        raise SymbolManifestError(f"file is missing: {path}")
    data = path.read_bytes()
    if _elf_header(data) is None:
        raise SymbolManifestError(f"could not read ELF sections: {path}")
    return {
        section.get("name", ""): section["size"] for section in _elf_sections_raw(data)
    }


def pe_debug_identity(path: Path) -> dict[str, Any] | None:
    """Read the PE CodeView RSDS record: PDB GUID, age and PDB name."""
    path = Path(path)
    try:
        data = path.read_bytes()
    except FileNotFoundError as error:
        raise SymbolManifestError(f"file is missing: {path}") from error
    if len(data) < 0x40 or data[:2] != b"MZ":
        return None
    pe_offset = struct.unpack_from("<I", data, 0x3C)[0]
    if data[pe_offset:pe_offset + 4] != b"PE\0\0":
        return None
    coff = pe_offset + 4
    number_of_sections = struct.unpack_from("<H", data, coff + 2)[0]
    optional_size = struct.unpack_from("<H", data, coff + 16)[0]
    optional = coff + 20
    magic = struct.unpack_from("<H", data, optional)[0]
    if magic == 0x20B:
        count_offset, directory_offset = optional + 108, optional + 112
    elif magic == 0x10B:
        count_offset, directory_offset = optional + 92, optional + 96
    else:
        return None
    directory_count = struct.unpack_from("<I", data, count_offset)[0]
    if directory_count <= 6:
        return None
    debug_rva, debug_size = struct.unpack_from("<II", data, directory_offset + 6 * 8)
    if not debug_rva or debug_size < 28:
        return None
    section_table = optional + optional_size
    for index in range(number_of_sections):
        entry = section_table + 40 * index
        virtual_size, virtual_address = struct.unpack_from("<II", data, entry + 8)
        raw_size, raw_pointer = struct.unpack_from("<II", data, entry + 16)
        span = max(virtual_size, raw_size)
        if virtual_address <= debug_rva < virtual_address + span:
            file_offset = raw_pointer + (debug_rva - virtual_address)
            break
    else:
        return None
    # IMAGE_DEBUG_DIRECTORY is 28 bytes: Characteristics, TimeDateStamp,
    # MajorVersion, MinorVersion, Type, SizeOfData, AddressOfRawData and
    # PointerToRawData (an absolute file offset). The directory holds one entry
    # per debug type, so the CodeView entry is selected, never assumed first.
    payload = None
    for index in range(debug_size // 28):
        entry = file_offset + 28 * index
        if entry + 28 > len(data):
            break
        (_, _, _, _, debug_type, size_of_data, _, pointer_to_raw) = struct.unpack_from(
            "<IIHHIIII", data, entry
        )
        if debug_type != 2 or size_of_data == 0 or pointer_to_raw == 0:
            continue
        if pointer_to_raw + size_of_data > len(data):
            continue
        payload = data[pointer_to_raw:pointer_to_raw + size_of_data]
        break
    if payload is None or payload[:4] != b"RSDS" or len(payload) < 24:
        return None
    guid_bytes = payload[4:20]
    age = struct.unpack_from("<I", payload, 20)[0]
    name = payload[24:].split(b"\0", 1)[0].decode("ascii", errors="replace")
    guid = (
        f"{int.from_bytes(guid_bytes[0:4], 'little'):08X}-"
        f"{int.from_bytes(guid_bytes[4:6], 'little'):04X}-"
        f"{int.from_bytes(guid_bytes[6:8], 'little'):04X}-"
        f"{guid_bytes[8:10].hex().upper()}-{guid_bytes[10:16].hex().upper()}"
    )
    return {"guid": guid, "age": age, "pdb_name": name}


def pdb_identity(path: Path) -> dict[str, Any] | None:
    """Read a portable PDB's own id: the ``#Pdb`` stream GUID and stamp.

    A portable PDB starts with the ``BSJB`` metadata signature; its ``#Pdb``
    stream holds the 20-byte id a linker records as the executable's CodeView
    GUID/age pair. A file that is not a portable PDB returns ``None``.
    """
    path = Path(path)
    try:
        data = path.read_bytes()
    except FileNotFoundError as error:
        raise SymbolManifestError(f"file is missing: {path}") from error
    if len(data) < 24 or data[:4] != b"BSJB":
        return None
    # ECMA-335 metadata root: signature, major, minor, reserved, version length,
    # the version string itself, then a flags field, the stream count and the
    # stream entries. Stream offsets are relative to the root, which is byte 0
    # of a PDB file.
    _, _, _, _, version_length = struct.unpack_from("<IHHII", data, 0)
    offset = 16 + version_length
    offset += (-offset) % 4
    if offset + 4 > len(data):
        return None
    offset += 2  # flags; a portable PDB writes zero here
    stream_count = struct.unpack_from("<H", data, offset)[0]
    offset += 2
    streams: dict[str, tuple[int, int]] = {}
    try:
        for _ in range(stream_count):
            if offset + 8 > len(data):
                return None
            relative, size = struct.unpack_from("<II", data, offset)
            offset += 8
            end = data.index(b"\0", offset)
            name = data[offset:end].decode("ascii", errors="replace")
            offset = end + 1
            offset += (-offset) % 4
            streams[name] = (relative, size)
    except ValueError:
        return None
    pdb = streams.get("#Pdb")
    if pdb is None or pdb[1] < 20:
        return None
    start = pdb[0]
    if start + 20 > len(data):
        return None
    guid_bytes = data[start:start + 16]
    stamp = struct.unpack_from("<I", data, start + 16)[0]
    guid = (
        f"{int.from_bytes(guid_bytes[0:4], 'little'):08X}-"
        f"{int.from_bytes(guid_bytes[4:6], 'little'):04X}-"
        f"{int.from_bytes(guid_bytes[6:8], 'little'):04X}-"
        f"{guid_bytes[8:10].hex().upper()}-{guid_bytes[10:16].hex().upper()}"
    )
    return {"guid": guid, "stamp": f"{stamp:08X}"}


def _basename(value: str) -> str:
    """The file name of a Windows or POSIX path a linker recorded."""
    return value.replace("\\", "/").rsplit("/", 1)[-1]


def identity_of(path: Path) -> dict[str, Any]:
    """Describe one executable's identity with every identity it carries."""
    path = Path(path)
    identity: dict[str, Any] = {"sha256": _sha256(path), "size": path.stat().st_size}
    buildinfo = go_buildinfo(path)
    if buildinfo is not None:
        identity["go_buildinfo"] = buildinfo
    uuid = macho_uuid(path)
    if uuid is not None:
        identity["uuid"] = uuid
    build_id = elf_build_id(path)
    if build_id is not None:
        identity["build_id"] = build_id
    debug = pe_debug_identity(path)
    if debug is not None:
        identity["pe_debug"] = debug
    return identity


def match_symbols(symbol_id: dict[str, Any], shipped_id: dict[str, Any]) -> dict[str, str]:
    """Prove one symbol file belongs to one shipped executable."""
    symbol_name = str(symbol_id.get("file") or symbol_id.get("sha256") or "<symbol>")
    if symbol_id.get("sha256") and symbol_id["sha256"] == shipped_id.get("sha256"):
        return {"kind": "byte-identical", "value": symbol_id["sha256"]}
    if symbol_id.get("uuid") and symbol_id["uuid"] == shipped_id.get("uuid"):
        return {"kind": "macho-uuid", "value": symbol_id["uuid"]}
    if symbol_id.get("build_id") and symbol_id["build_id"] == shipped_id.get("build_id"):
        return {"kind": "elf-build-id", "value": symbol_id["build_id"]}
    shipped_debug = shipped_id.get("pe_debug")
    symbol_pdb = symbol_id.get("pdb_id")
    if shipped_debug and symbol_pdb:
        # Bind on the identity the files actually carry: the PDB's own id must be
        # the GUID/age pair the executable's CodeView record names, and the names
        # must agree once both sides are reduced to a file name.
        pdb_name = symbol_id.get("pdb_name") or symbol_name
        if (
            symbol_pdb.get("guid") == shipped_debug.get("guid")
            and _basename(str(shipped_debug.get("pdb_name") or "")) == _basename(str(pdb_name))
        ):
            return {
                "kind": "pdb-guid",
                "value": f"{shipped_debug['guid']}/{shipped_debug['age']}",
            }
    if symbol_id.get("go_buildinfo") and symbol_id["go_buildinfo"] == shipped_id.get("go_buildinfo"):
        return {"kind": "go-buildinfo", "value": json.dumps(symbol_id["go_buildinfo"], sort_keys=True)}
    raise SymbolManifestError(f"symbol identity does not match shipped executable: {symbol_name}")


def diagnostics_files(product_root: Path) -> dict[str, str]:
    """Hash the files that carry the cross-platform diagnostics this release has."""
    root = Path(product_root)
    missing = [relative for relative in DIAGNOSTIC_FILES if not (root / relative).is_file()]
    if missing:
        raise SymbolManifestError(f"diagnostics source is missing: {', '.join(missing)}")
    return {relative: _sha256(root / relative) for relative in DIAGNOSTIC_FILES}


def capture_toolchain(
    commands: dict[str, Sequence[str]], *, runner: Callable[..., Any] = subprocess.run,
) -> dict[str, str]:
    """Run each named probe once and keep its first output line."""
    captured: dict[str, str] = {}
    for name, command in commands.items():
        result = _run(command, runner=runner)
        if result.returncode != 0:
            raise SymbolManifestError(f"toolchain probe failed: {name}")
        lines = [line for line in (result.stdout or "").splitlines() if line.strip()]
        if not lines:
            raise SymbolManifestError(f"toolchain probe produced no output: {name}")
        captured[name] = lines[0].strip()
    return captured


def build_identity(
    *,
    repository: str,
    workflow_path: str,
    run_id: int,
    run_number: int,
    run_attempt: int,
    source_sha: str,
    source_tree: str,
    version: str,
    android_version_code: int,
    apple_build_number: int,
    platform: str,
    architecture: str,
    toolchain: dict[str, str],
    build_flags: dict[str, Any],
    diagnostics: dict[str, Any],
    diagnostic_variant: dict[str, Any] | None,
    package: dict[str, Any],
    binaries: list[dict[str, Any]],
    third_party: list[dict[str, Any]],
    configuration: str = "Release",
) -> dict[str, Any]:
    """Assemble one build-identity document (validated by :func:`write_identity`)."""
    document = {
        "schema": SCHEMA,
        "kind": KIND,
        "repository": repository,
        "workflow_path": workflow_path,
        "run_id": run_id,
        "run_number": run_number,
        "run_attempt": run_attempt,
        "source_sha": source_sha,
        "source_tree": source_tree,
        "version": version,
        "android_version_code": android_version_code,
        "apple_build_number": apple_build_number,
        "platform": platform,
        "architecture": architecture,
        "configuration": configuration,
        "package": package,
        "toolchain": toolchain,
        "build_flags": build_flags,
        "diagnostics": diagnostics,
        "diagnostic_variant": diagnostic_variant,
        "binaries": binaries,
        "third_party": third_party,
    }
    return document


def _json_bytes(document: dict[str, Any]) -> bytes:
    return (
        json.dumps(document, ensure_ascii=True, separators=(",", ":"), sort_keys=True) + "\n"
    ).encode("utf-8")


def _validate_metadata(document: dict[str, Any]) -> None:
    if set(document) != DOCUMENT_KEYS:
        raise SymbolManifestError("build identity has unexpected or missing fields")
    if document.get("schema") != SCHEMA or document.get("kind") != KIND:
        raise SymbolManifestError("build identity has an unsupported schema or kind")
    if document.get("platform") not in PLATFORMS:
        raise SymbolManifestError("build identity names an unsupported platform")
    if not isinstance(document.get("source_sha"), str) or not SHA40.fullmatch(document["source_sha"]):
        raise SymbolManifestError("build identity needs a full lowercase source commit")
    if not isinstance(document.get("source_tree"), str) or not SHA40.fullmatch(document["source_tree"]):
        raise SymbolManifestError("build identity needs a full lowercase source tree")
    if not isinstance(document.get("version"), str) or not VERSION.fullmatch(document["version"]):
        raise SymbolManifestError("build identity needs a canonical X.Y.Z version")
    for key in ("run_id", "run_number", "run_attempt", "android_version_code", "apple_build_number"):
        value = document.get(key)
        if type(value) is not int or value < 0:
            raise SymbolManifestError(f"build identity has an invalid {key}")
    package = document.get("package")
    if (
        not isinstance(package, dict) or set(package) != {"name", "sha256", "size"}
        or not isinstance(package.get("sha256"), str) or not SHA256.fullmatch(package["sha256"])
        or type(package.get("size")) is not int or package["size"] <= 0
    ):
        raise SymbolManifestError("build identity has an invalid package record")
    diagnostics = document.get("diagnostics")
    if (
        not isinstance(diagnostics, dict)
        or set(diagnostics) != {"enabled", "feature_flags", "files", "constants"}
        or diagnostics.get("feature_flags") is not None
        or not isinstance(diagnostics.get("files"), dict)
        or not diagnostics["files"]
    ):
        raise SymbolManifestError("build identity has an invalid diagnostics record")
    for relative, digest in diagnostics["files"].items():
        if not isinstance(digest, str) or not SHA256.fullmatch(digest):
            raise SymbolManifestError(f"diagnostics digest is invalid: {relative}")
    variant = document.get("diagnostic_variant")
    if variant is not None and (
        not isinstance(variant, dict)
        or set(variant) != {"name", "sha256", "application_id", "debuggable"}
        or not isinstance(variant.get("sha256"), str) or not SHA256.fullmatch(variant["sha256"])
    ):
        raise SymbolManifestError("build identity has an invalid diagnostic variant")
    if document.get("platform") == "android" and variant is None:
        raise SymbolManifestError("Android build identity must name its diagnostic variant")


def write_identity(directory: Path, document: dict[str, Any]) -> Path:
    """Write ``build-identity.json`` into a bundle directory after validating it."""
    directory = Path(directory)
    _validate_metadata(document)
    for binary in document["binaries"]:
        if not isinstance(binary, dict) or set(binary) != BINARY_KEYS:
            raise SymbolManifestError("build identity has an invalid binary record")
        relative = binary.get("file")
        if not isinstance(relative, str) or not relative:
            raise SymbolManifestError("build identity binary record has no file")
        observed = sha256_of(directory / relative)
        if observed != binary.get("sha256"):
            raise SymbolManifestError(f"binary digest changed before it was recorded: {relative}")
        if binary.get("match", {}).get("kind") not in MATCH_KINDS:
            raise SymbolManifestError(f"binary record has an unsupported match kind: {relative}")
    target = directory / "build-identity.json"
    target.write_bytes(_json_bytes(document))
    return target


def verify_identity(directory: Path, *, package_sha256: str | None = None) -> dict[str, Any]:
    """Verify a bundle's manifest against the bundle bytes on disk."""
    directory = Path(directory)
    manifest = directory / "build-identity.json"
    try:
        document = json.loads(manifest.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise SymbolManifestError(f"build identity is unreadable: {manifest}") from error
    if not isinstance(document, dict):
        raise SymbolManifestError("build identity must contain a JSON object")
    _validate_metadata(document)
    if package_sha256 is not None and package_sha256 != document["package"]["sha256"]:
        raise SymbolManifestError("package digest does not match the shipped package")
    for binary in document["binaries"]:
        if set(binary) != BINARY_KEYS:
            raise SymbolManifestError("build identity has an invalid binary record")
        relative = binary["file"]
        observed = _sha256(directory / relative)
        if observed != binary["sha256"]:
            raise SymbolManifestError(f"binary bytes changed since the identity was written: {relative}")
        shipped = _shipped_identity(directory / relative)
        recorded_digests = binary["symbol_digests"]
        if set(recorded_digests) != set(binary["symbol_files"]):
            raise SymbolManifestError(f"symbol digest set is incomplete: {relative}")
        for symbol_file in binary["symbol_files"]:
            symbol_path = directory / symbol_file
            if not symbol_path.exists():
                raise SymbolManifestError(f"symbol file is missing: {symbol_file}")
            observed_digest = _sha256(_symbol_member(symbol_path))
            if observed_digest != recorded_digests[symbol_file]:
                raise SymbolManifestError(f"symbol bytes changed since the identity was written: {symbol_file}")
        primary = next(
            (name for name in binary["symbol_files"] if not name.endswith(".txt")), None,
        )
        if primary is None:
            raise SymbolManifestError(f"binary has no identity-bearing symbol file: {relative}")
        observed_match = match_symbols(_symbol_identity(directory / primary), shipped)
        recorded = binary["match"]
        # A retained file that is byte-identical to the binary it documents
        # satisfies any recorded binding: sameness is the strongest match.
        if observed_match["kind"] == "byte-identical":
            continue
        if observed_match["kind"] != recorded["kind"] or observed_match["value"] != recorded["value"]:
            raise SymbolManifestError(
                f"symbol identity does not match shipped executable: {primary}"
            )
    return document


def _tar_bundle(root: Path, output: Path, top: str) -> Path:
    """Tar one staged bundle with a single top-level directory."""
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_name(f".{output.name}.tmp")
    try:
        with tarfile.open(temporary, mode="w:gz", format=tarfile.PAX_FORMAT) as tar:
            tar.add(root, arcname=top, recursive=True)
        with tarfile.open(temporary, mode="r:gz") as tar:
            roots = {member.name.split("/", 1)[0] for member in tar.getmembers()}
        if roots != {top}:
            raise SymbolManifestError("symbol bundle must contain one top-level directory")
        temporary.replace(output)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise
    return output


def _stage(root: Path, relative: str, source: Path) -> Path:
    """Copy one file into the bundle under ``relative``."""
    target = root / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    if not Path(source).is_file():
        raise SymbolManifestError(f"{Path(source).name} not found in the build output")
    shutil.copy2(source, target)
    return target


def _stage_dir(root: Path, relative: str, source: Path) -> Path:
    """Copy one directory (a dSYM bundle) into the bundle under ``relative``."""
    if not Path(source).is_dir():
        raise SymbolManifestError(f"{Path(source).name} not found in the build output")
    target = root / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(source, target)
    return target


def _binary_record(
    root: Path, *, role: str, file: Path, symbol_files: list[Path], match: dict[str, str],
) -> dict[str, Any]:
    relative = file.relative_to(root).as_posix()
    return {
        "role": role,
        "file": relative,
        "sha256": _sha256(file),
        "size": file.stat().st_size,
        "identity": {"kind": match["kind"], "value": match["value"]},
        "symbol_files": [symbol.relative_to(root).as_posix() for symbol in symbol_files],
        "symbol_digests": {
            symbol.relative_to(root).as_posix(): _sha256(_symbol_member(symbol))
            for symbol in symbol_files
        },
        "match": match,
    }


def _symbol_member(path: Path) -> Path:
    """Return the file a symbol entry's digest covers (a dSYM's DWARF member)."""
    if path.is_dir():
        dwarf = next(path.rglob("DWARF"), None)
        if dwarf is None:
            raise SymbolManifestError(f"symbol bundle has no DWARF directory: {path.name}")
        member = next((item for item in sorted(dwarf.iterdir()) if item.is_file()), None)
        if member is None:
            raise SymbolManifestError(f"symbol bundle is empty: {path.name}")
        return member
    return path


def _buildinfo_capture(root: Path, file: Path, name: str | None = None) -> Path | None:
    """Write the ``go version -m`` capture for one binary, or nothing if not Go."""
    if go_buildinfo(file) is None:
        return None
    go = go_executable()
    if go is None:
        raise SymbolManifestError("go is required to capture build info")
    target = root / "buildinfo" / f"{name or file.name}.txt"
    target.parent.mkdir(parents=True, exist_ok=True)
    result = _run([go, "version", "-m", str(file)])
    # The first line names the path the tool was given, which is a staging
    # directory and changes between runs; the entries below it are the identity.
    lines = (result.stdout or "").splitlines()
    body = "\n".join(lines[1:]) if len(lines) > 1 else ""
    target.write_text(body + ("\n" if body else ""), encoding="utf-8")
    return target


def _shipped_identity(file: Path) -> dict[str, Any]:
    return {
        "sha256": _sha256(file),
        "uuid": macho_uuid(file),
        "build_id": elf_build_id(file),
        "pe_debug": pe_debug_identity(file),
        "go_buildinfo": go_buildinfo(file),
    }


def _symbol_identity(path: Path) -> dict[str, Any]:
    """Describe one symbol file: a Mach-O dSYM bundle or a plain file."""
    if path.is_dir():
        dwarf = next(path.rglob("DWARF"), None)
        if dwarf is None:
            raise SymbolManifestError(f"symbol bundle has no DWARF directory: {path.name}")
        member = next((item for item in sorted(dwarf.iterdir()) if item.is_file()), None)
        if member is None:
            raise SymbolManifestError(f"symbol bundle is empty: {path.name}")
        return {"file": str(path), "sha256": _sha256(member), "uuid": macho_uuid(member)}
    if path.suffix.lower() == ".pdb":
        return {"file": str(path), "sha256": _sha256(path), "pdb_name": path.name,
                "pdb_id": pdb_identity(path)}
    if path.suffix.lower() == ".txt":
        # A build-info capture belongs to its binary; it is evidence, not an
        # identity of its own, so it is bound by the record it sits beside.
        return {"file": str(path), "sha256": _sha256(path)}
    return {
        "file": str(path),
        "sha256": _sha256(path),
        "uuid": macho_uuid(path),
        "build_id": elf_build_id(path),
        "pe_debug": pe_debug_identity(path),
        "go_buildinfo": go_buildinfo(path),
    }


def _bind(file: Path, symbol_files: list[Path]) -> dict[str, str]:
    """Prove the first symbol file belongs to ``file``; return the match."""
    shipped = _shipped_identity(file)
    for symbol_path in symbol_files:
        return match_symbols(_symbol_identity(symbol_path), shipped)
    raise SymbolManifestError(f"binary has no symbol files: {file.name}")


def collect_desktop_symbols(
    *,
    platform: str,
    architecture: str,
    service_directory: Path,
    frontend_directory: Path | None,
    output_dir: Path,
    version: str,
    source_sha: str,
    source_tree: str,
    package: Path,
    toolchain: dict[str, str],
    build_flags: dict[str, Any],
    diagnostics: dict[str, Any],
    third_party: Iterable[dict[str, Any]],
    debug: bool = False,
    repository: str = "DobbyVPN/DobbyVPN",
    workflow_path: str = ".github/workflows/build.yml",
    run_id: int = 0,
    run_number: int = 0,
    run_attempt: int = 0,
    android_version_code: int = 0,
    apple_build_number: int = 0,
) -> Path:
    """Stage, record, verify and tar one desktop platform's symbol bundle."""
    service_directory = Path(service_directory)
    root = Path(tempfile.mkdtemp(prefix=f"dobbyvpn-symbols-{platform}-"))
    try:
        binaries: list[dict[str, Any]] = []
        if platform == "macos":
            for name, role in (
                ("DobbyVPNMacApp", "frontend"),
                ("dobbyvpn-backend", "backend"),
                ("dobby-cli", "cli"),
            ):
                staged = _stage(root, f"binaries/{name}", service_directory / name)
                dsym = _stage_dir(
                    root, f"dsyms/{name}.dSYM", service_directory / "DebugSymbols" / f"{name}.dSYM",
                )
                match = _bind(staged, [dsym])
                capture = _buildinfo_capture(root, staged)
                symbols = [dsym] + ([capture] if capture else [])
                binaries.append(_binary_record(
                    root, role=role, file=staged, symbol_files=symbols, match=match,
                ))
        elif platform == "windows":
            frontend = Path(frontend_directory or service_directory / "frontend")
            # The publish output has two frontend files with different symbol
            # owners. DobbyVPN.dll is the managed assembly whose linker emitted
            # DobbyVPN.pdb; DobbyVPN.exe is the .NET apphost, whose CodeView
            # record points at the runtime's own apphost.pdb, which is not part
            # of this product's publish output. So the PDB binds to the assembly
            # by its own identity, and the apphost is retained the way the Go
            # binaries are: as the exact file that shipped.
            assembly_pdb = frontend / "DobbyVPN.pdb"
            staged_pdb = _stage(root, "pdbs/DobbyVPN.pdb", assembly_pdb)
            for name, role in (
                ("DobbyVPN.exe", "frontend"),
                ("DobbyVPN.dll", "frontend-assembly"),
            ):
                staged = _stage(root, f"binaries/{name}", frontend / name)
                if name == "DobbyVPN.dll":
                    symbol_files = [staged_pdb]
                else:
                    # The apphost's own record names a runtime PDB this product
                    # does not publish, so its retained copy is the identity.
                    symbol_files = [_stage(root, f"binaries/{name}.unstripped", frontend / name)]
                match = _bind(staged, symbol_files)
                binaries.append(_binary_record(
                    root, role=role, file=staged, symbol_files=symbol_files, match=match,
                ))
            for name, role in (
                ("dobbyvpn-backend.exe", "backend"),
                ("dobby-cli.exe", "cli"),
            ):
                staged = _stage(root, f"binaries/{name}", service_directory / name)
                capture = _buildinfo_capture(root, staged)
                if capture is None:
                    raise SymbolManifestError(f"{name} carries no Go build identity")
                match = _bind(staged, [staged])
                binaries.append(_binary_record(
                    root, role=role, file=staged, symbol_files=[staged, capture], match=match,
                ))
        elif platform == "linux":
            for name, role in (
                ("dobbyvpn-backend", "backend"),
                ("dobby-cli", "cli"),
                ("libdobby_bridge.so", "bridge"),
            ):
                staged = _stage(root, f"binaries/{name}", service_directory / name)
                if elf_sections(staged).get(".symtab", 0) == 0:
                    raise SymbolManifestError(f"{name} was stripped before retention")
                capture = _buildinfo_capture(root, staged)
                match = _bind(staged, [staged])
                binaries.append(_binary_record(
                    root, role=role, file=staged,
                    symbol_files=[staged] + ([capture] if capture else []), match=match,
                ))
        else:
            raise SymbolManifestError(f"unsupported desktop platform for symbols: {platform}")
        document = build_identity(
            repository=repository, workflow_path=workflow_path, run_id=run_id,
            run_number=run_number, run_attempt=run_attempt, source_sha=source_sha,
            source_tree=source_tree, version=version,
            android_version_code=android_version_code, apple_build_number=apple_build_number,
            platform=platform, architecture=architecture, toolchain=toolchain,
            build_flags=build_flags, diagnostics=diagnostics, diagnostic_variant=None,
            package={
                "name": Path(package).name,
                "sha256": _sha256(Path(package)),
                "size": Path(package).stat().st_size,
            },
            binaries=binaries, third_party=list(third_party),
        )
        write_identity(root, document)
        verify_identity(root)
        stem = f"dobbyvpn-symbols-{platform}-{architecture}"
        if debug:
            stem += "-debug"
        return _tar_bundle(root, Path(output_dir) / f"{stem}.tar.gz", stem)
    finally:
        shutil.rmtree(root, ignore_errors=True)


def collect_android_symbols(
    *,
    go_libs: Path,
    stripped_libs: Path,
    release_apk: Path,
    debug_apk: Path,
    output: Path,
    version: str,
    source_sha: str,
    source_tree: str,
    android_version_code: int,
    toolchain: dict[str, str],
    build_flags: dict[str, Any],
    diagnostics: dict[str, Any],
    repository: str = "DobbyVPN/DobbyVPN",
    workflow_path: str = ".github/workflows/build.yml",
    run_id: int = 0,
    run_number: int = 0,
    run_attempt: int = 0,
    apple_build_number: int = 0,
) -> Path:
    """Stage, record, verify and tar the Android ABI symbol bundle."""
    import zipfile

    root = Path(tempfile.mkdtemp(prefix="dobbyvpn-symbols-android-"))
    try:
        abis = sorted(
            item.name for item in Path(stripped_libs).iterdir() if item.is_dir()
        )
        if not abis:
            raise SymbolManifestError(f"no stripped ABI libs under {stripped_libs}")
        apk_bytes = Path(release_apk).read_bytes()
        binaries: list[dict[str, Any]] = []
        with zipfile.ZipFile(release_apk) as package:
            for abi in abis:
                kept = Path(go_libs) / abi / "libdobby_vpn.so"
                shipped = Path(stripped_libs) / abi / "libdobby_vpn.so"
                for path in (kept, shipped):
                    if not path.is_file():
                        raise SymbolManifestError(f"libdobby_vpn.so missing for {abi}")
                kept_sections = elf_sections(kept)
                shipped_sections = elf_sections(shipped)
                missing = [name for name in (".debug_info", ".symtab") if not kept_sections.get(name)]
                if missing:
                    raise SymbolManifestError(f"kept lib for {abi} carries no {', '.join(missing)}")
                if [name for name in (".debug_info", ".symtab") if shipped_sections.get(name)]:
                    raise SymbolManifestError(f"shipped lib for {abi} still carries debug sections")
                kept_id = elf_build_id(kept)
                if not kept_id or kept_id != elf_build_id(shipped):
                    raise SymbolManifestError(f"build identity differs between kept and shipped lib for {abi}")
                staged = _stage(root, f"native/{abi}/libdobby_vpn.so", kept)
                packaged = package.read(f"lib/{abi}/libdobby_vpn.so")
                match = match_symbols(
                    _shipped_identity(staged),
                    {
                        "sha256": hashlib.sha256(packaged).hexdigest(),
                        "build_id": elf_build_id(shipped),
                        "go_buildinfo": go_buildinfo(shipped),
                    },
                )
                capture = _buildinfo_capture(root, staged, name=f"{abi}-libdobby_vpn.so")
                binaries.append(_binary_record(
                    root, role=f"backend-{abi}", file=staged,
                    symbol_files=[staged] + ([capture] if capture else []), match=match,
                ))
        document = build_identity(
            repository=repository, workflow_path=workflow_path, run_id=run_id,
            run_number=run_number, run_attempt=run_attempt, source_sha=source_sha,
            source_tree=source_tree, version=version,
            android_version_code=android_version_code, apple_build_number=apple_build_number,
            platform="android", architecture="+".join(abis), toolchain=toolchain,
            build_flags=build_flags, diagnostics=diagnostics,
            diagnostic_variant={
                "name": Path(debug_apk).name,
                "sha256": _sha256(Path(debug_apk)),
                "application_id": "com.dobby.vpn.debug",
                "debuggable": True,
            },
            package={
                "name": Path(release_apk).name,
                "sha256": hashlib.sha256(apk_bytes).hexdigest(),
                "size": len(apk_bytes),
            },
            binaries=binaries, third_party=[],
        )
        write_identity(root, document)
        verify_identity(root)
        return _tar_bundle(root, Path(output), "dobbyvpn-symbols-android")
    finally:
        shutil.rmtree(root, ignore_errors=True)


ANDROID_BUILD_FLAGS = {
    "go_ldflags": (
        "-buildid= -X core/buildinfo.Version/Commit/Configuration"
        " -checklinkname=0 -extldflags=-static-libstdc++ -Wl,--build-id=sha1"
    ),
    "go_tags": "android,accessibility,static",
    "go_toolchain": "local",
}


def android_toolchain(product_root: Path, *, java_version: str | None = None) -> dict[str, str]:
    """Reuse the product's own Android dependency pins for the toolchain block."""
    root = Path(product_root)
    sys.path.insert(0, str(root / ".github" / "scripts" / "android"))
    try:
        import android_dependency_provenance as provenance
    finally:
        sys.path.pop(0)
    pins = provenance.dependency_pins(root)
    toolchain = {
        "java_version": java_version or str(pins["java_major"]),
        "android_build_tools": str(pins["android_build_tools"]),
        "android_ndk": str(pins["android_ndk"]),
        "go_version": str(pins["go_version"]),
        "go_source_commit": str(pins["go_source_commit"]),
        "gradle": str(pins["gradle_version"]),
    }
    for name in ("kotlin", "agp", "agp_version", "compose_bom"):
        if name in pins and pins[name] not in (None, ""):
            toolchain[name] = str(pins[name])
    return toolchain


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    verify = commands.add_parser("verify", help="verify one expanded symbol bundle")
    verify.add_argument("--directory", type=Path, required=True)
    verify.add_argument("--package-sha256")
    android = commands.add_parser("create-android", help="create the Android ABI symbol bundle")
    android.add_argument("--go-libs", type=Path, required=True)
    android.add_argument("--stripped-libs", type=Path, required=True)
    android.add_argument("--release-apk", type=Path, required=True)
    android.add_argument("--debug-apk", type=Path, required=True)
    android.add_argument("--output", type=Path, required=True)
    android.add_argument("--version", required=True)
    android.add_argument("--source-sha", required=True)
    android.add_argument("--source-tree", required=True)
    android.add_argument("--android-version-code", type=int, required=True)
    android.add_argument("--apple-build-number", type=int, default=0)
    android.add_argument("--product-root", type=Path, default=None)
    android.add_argument("--java-version")
    args = parser.parse_args(argv)
    try:
        if args.command == "verify":
            document = verify_identity(args.directory, package_sha256=args.package_sha256)
            print(json.dumps({"verified": document["platform"], "binaries": len(document["binaries"])}))
        else:
            product_root = args.product_root or Path(__file__).resolve().parents[3]
            produced = collect_android_symbols(
                go_libs=args.go_libs, stripped_libs=args.stripped_libs,
                release_apk=args.release_apk, debug_apk=args.debug_apk, output=args.output,
                version=args.version, source_sha=args.source_sha, source_tree=args.source_tree,
                android_version_code=args.android_version_code,
                apple_build_number=args.apple_build_number,
                toolchain=android_toolchain(product_root, java_version=args.java_version),
                build_flags=dict(ANDROID_BUILD_FLAGS),
                diagnostics={
                    "enabled": True, "feature_flags": None,
                    "files": diagnostics_files(product_root),
                    "constants": DIAGNOSTIC_CONSTANTS,
                },
            )
            print(produced)
    except (SymbolManifestError, OSError, ValueError, KeyError) as error:
        print(f"symbol manifest error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
