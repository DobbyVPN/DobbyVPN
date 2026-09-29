#!/usr/bin/env python3
"""Write Android build provenance from the tracked dependency declarations."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
from typing import Any


SHA40 = re.compile(r"^[0-9a-f]{40}$")
SHA256 = re.compile(r"^[0-9a-f]{64}$")
SCHEMA = 1
KIND = "dobbyvpn.android.dependency-spec"
REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_SPEC = REPOSITORY_ROOT / ".github/scripts/android/dependency-spec.json"
DECLARED_INPUTS = (
    ".go-version",
    "ui/android/settings.gradle.kts",
    "ui/android/build.gradle.kts",
    "ui/android/app/build.gradle.kts",
    "ui/android/gradle/wrapper/gradle-wrapper.properties",
    "ui/android/gradle/wrapper/gradle-wrapper.jar",
    "ui/android/gradle.properties",
    "core/go.mod",
    "core/go.sum",
)


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _regular_file_beneath(root: Path, relative: str, label: str) -> tuple[Path, str, int]:
    path = root / relative
    if not path.is_file():
        raise ValueError(f"{label} is unavailable: {relative}")
    size = path.stat().st_size
    return path, _sha256(path), size


def _read_spec(path: Path) -> dict[str, Any]:
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency specification is not valid JSON") from error
    if not isinstance(document, dict):
        raise ValueError("dependency specification must be an object")
    if set(document) != {"schema", "kind", "go", "android"}:
        raise ValueError("dependency specification has unexpected or missing fields")
    if document["schema"] != SCHEMA or document["kind"] != KIND:
        raise ValueError("dependency specification has an unsupported schema")
    go = document["go"]
    if not isinstance(go, dict) or set(go) != {"source_commit"}:
        raise ValueError("Go source pin is incomplete")
    if not isinstance(go["source_commit"], str) or not SHA40.fullmatch(go["source_commit"]):
        raise ValueError("Go source commit must be a full lowercase Git SHA")
    android = document["android"]
    if not isinstance(android, dict) or set(android) != {"build_tools"}:
        raise ValueError("Android build-tools pin is incomplete")
    if not isinstance(android["build_tools"], str) or not re.fullmatch(r"\d+(?:\.\d+){2}", android["build_tools"]):
        raise ValueError("Android build-tools version must use X.Y.Z format")
    return document


def _read_go_version(source_root: Path) -> str:
    try:
        version = (source_root / ".go-version").read_text(encoding="utf-8").strip()
    except (OSError, UnicodeError) as error:
        raise ValueError(".go-version is unavailable") from error
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError(f".go-version must contain a Go release version, got {version!r}")
    return version


def _read_mobile_module(source_root: Path) -> tuple[str, str]:
    try:
        go_mod = (source_root / "core/go.mod").read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise ValueError("core/go.mod is unavailable") from error
    matches = re.findall(
        r"(?m)^\s*([A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*/mobile)\s+(v[^\s]+)(?:\s+//.*)?$",
        go_mod,
    )
    if len(matches) != 1:
        raise ValueError("core/go.mod must declare exactly one x/mobile module version")
    return matches[0]


def _wrapper_values(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeError) as error:
        raise ValueError("Gradle wrapper properties are unavailable") from error
    for line in lines:
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key] = value.replace("\\:", ":")
    url = values.get("distributionUrl", "")
    match = re.fullmatch(
        r"https://services\.gradle\.org/distributions/gradle-(\d+(?:\.\d+)+)-bin\.zip",
        url,
    )
    checksum = values.get("distributionSha256Sum", "").lower()
    if match is None or not SHA256.fullmatch(checksum):
        raise ValueError("Gradle wrapper must declare a versioned HTTPS distribution and SHA-256")
    return {"url": url, "version": match.group(1), "sha256": checksum}


def _read_android_gradle_values(path: Path) -> tuple[int, str, int]:
    try:
        build_file = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise ValueError("Android application Gradle build file is unavailable") from error
    java_versions = set(re.findall(r"JavaVersion\.VERSION_(\d+)", build_file))
    jvm_targets = set(re.findall(r'jvmTarget\s*=\s*"(\d+)"', build_file))
    ndk_versions = set(re.findall(r'pinnedAndroidNdkVersion\s*=\s*"([^"]+)"', build_file))
    compile_sdks = set(re.findall(r"(?m)^\s*compileSdk\s*=\s*(\d+)\s*$", build_file))
    if len(java_versions) != 1 or jvm_targets != java_versions:
        raise ValueError("Android Gradle Java compatibility declarations must agree on one major version")
    if len(ndk_versions) != 1:
        raise ValueError("Android Gradle build must declare one pinned NDK version")
    if len(compile_sdks) != 1:
        raise ValueError("Android Gradle build must declare one compile SDK version")
    return int(next(iter(java_versions))), next(iter(ndk_versions)), int(next(iter(compile_sdks)))


def dependency_pins(source_root: Path, spec_path: Path | None = None) -> dict[str, str | int]:
    spec = _read_spec(spec_path or source_root / ".github/scripts/android/dependency-spec.json")
    mobile_module, mobile_version = _read_mobile_module(source_root)
    gradle = _wrapper_values(source_root / "ui/android/gradle/wrapper/gradle-wrapper.properties")
    java_major, android_ndk, compile_sdk = _read_android_gradle_values(
        source_root / "ui/android/app/build.gradle.kts"
    )
    return {
        "android_build_tools": spec["android"]["build_tools"],
        "android_ndk": android_ndk,
        "android_compile_sdk": compile_sdk,
        "go_source_commit": spec["go"]["source_commit"],
        "go_version": _read_go_version(source_root),
        "gradle_sha256": gradle["sha256"],
        "gradle_url": gradle["url"],
        "gradle_version": gradle["version"],
        "java_major": java_major,
        "mobile_module": mobile_module,
        "mobile_version": mobile_version,
    }


# Keep the existing import interface for the Android reproducibility checker.
# Values are read from their owning source files instead of copied here.
_DEFAULT_PINS = dependency_pins(REPOSITORY_ROOT, DEFAULT_SPEC)
ANDROID_BUILD_TOOLS = str(_DEFAULT_PINS["android_build_tools"])
ANDROID_NDK = str(_DEFAULT_PINS["android_ndk"])
ANDROID_COMPILE_SDK = int(_DEFAULT_PINS["android_compile_sdk"])
GO_SOURCE_COMMIT = str(_DEFAULT_PINS["go_source_commit"])
GO_VERSION = str(_DEFAULT_PINS["go_version"])
GRADLE_VERSION = str(_DEFAULT_PINS["gradle_version"])
JAVA_MAJOR = int(_DEFAULT_PINS["java_major"])
MOBILE_MODULE = str(_DEFAULT_PINS["mobile_module"])
MOBILE_VERSION = str(_DEFAULT_PINS["mobile_version"])


def _verify_external_gradle_distribution(
    archive: Path, root: Path, pins: dict[str, str | int]
) -> dict[str, object]:
    if not archive.is_file():
        raise ValueError("external Gradle archive is unavailable")
    archive_sha256 = _sha256(archive)
    gradle_sha256 = str(pins["gradle_sha256"])
    if archive_sha256 != gradle_sha256:
        raise ValueError("external Gradle archive SHA-256 does not match the Gradle wrapper")
    gradle_entry = root / "bin/gradle"
    if not gradle_entry.is_file() or not (gradle_entry.stat().st_mode & 0o100):
        raise ValueError("external Gradle root does not contain an executable bin/gradle")
    return {
        "archive_sha256": archive_sha256,
        "sha256": gradle_sha256,
        "source": "external_verified_archive",
        "url": pins["gradle_url"],
        "version": pins["gradle_version"],
    }


def _spec_location(source_root: Path, spec_path: Path) -> tuple[Path, str]:
    try:
        relative = spec_path.absolute().relative_to(source_root.absolute()).as_posix()
    except ValueError as error:
        raise ValueError("dependency specification must be beneath its source root") from error
    path, _digest, _size = _regular_file_beneath(source_root, relative, "dependency specification")
    return path, relative


def _infer_source_root(spec_path: Path) -> Path:
    path = spec_path.absolute()
    try:
        if path.as_posix().endswith("/.github/scripts/android/dependency-spec.json"):
            return path.parents[3]
    except IndexError:
        pass
    raise ValueError("--spec must be at .github/scripts/android/dependency-spec.json or --source-root must be supplied")


def create_manifest(
    source_root: Path,
    source_commit: str,
    source_tree: str,
    spec_path: Path,
    *,
    java_version: str | None = None,
    gradle_archive: Path | None = None,
    gradle_root: Path | None = None,
    go_build_origin: str = "source_tree",
    go_binary_sha256: str | None = None,
) -> dict[str, object]:
    if not SHA40.fullmatch(source_commit) or not SHA40.fullmatch(source_tree):
        raise ValueError("source commit/tree must be full lowercase Git identities")
    if (gradle_archive is None) != (gradle_root is None):
        raise ValueError("external Gradle archive and root proof must be supplied together")
    if go_build_origin not in {"source_tree", "binary"}:
        raise ValueError("Go build origin must be source_tree or binary")
    if go_build_origin == "source_tree":
        if go_binary_sha256 is not None:
            raise ValueError("a source-built Go toolchain must not be identified as a binary install")
    elif not isinstance(go_binary_sha256, str) or not SHA256.fullmatch(go_binary_sha256):
        raise ValueError("binary Go toolchain requires its lowercase executable SHA-256")

    spec_file, spec_relative = _spec_location(source_root, spec_path)
    pins = dependency_pins(source_root, spec_file)
    expected_java = int(pins["java_major"])
    if java_version is None:
        java_version = str(expected_java)
    if not isinstance(java_version, str) or not (
        java_version == str(expected_java) or java_version.startswith(f"{expected_java}.")
    ):
        raise ValueError(f"observed Java version must have major {expected_java}, got {java_version!r}")

    inputs: list[dict[str, object]] = []
    for relative in DECLARED_INPUTS:
        _path, digest, size = _regular_file_beneath(source_root, relative, "dependency input")
        inputs.append({"path": relative, "sha256": digest, "size_bytes": size})
    gradle_distribution = (
        _verify_external_gradle_distribution(gradle_archive, gradle_root, pins)
        if gradle_archive is not None and gradle_root is not None
        else {
            "source": "wrapper_checksum",
            "url": pins["gradle_url"],
            "sha256": pins["gradle_sha256"],
            "version": pins["gradle_version"],
        }
    )
    spec_sha256 = _sha256(spec_file)
    inputs.append({
        "path": spec_relative,
        "sha256": spec_sha256,
        "size_bytes": spec_file.stat().st_size,
    })
    toolchain: dict[str, object] = {
        "java_major": expected_java,
        "java_version": java_version,
        "android_build_tools": pins["android_build_tools"],
        "android_ndk": pins["android_ndk"],
        "go_version": pins["go_version"],
    }
    if go_build_origin == "source_tree":
        toolchain["go_source_commit"] = pins["go_source_commit"]
    else:
        toolchain.update({
            "go_build_origin": "binary_executable",
            "go_source_commit": None,
            "go_binary_sha256": go_binary_sha256,
        })

    return {
        "schema": SCHEMA,
        "kind": "dobbyvpn.android.dependency-provenance",
        "repository": "DobbyVPN/DobbyVPN",
        "source": {"commit": source_commit, "tree": source_tree},
        "dependency_provenance": "tracked_dependency_spec",
        "resolution": {
            "mode": "pinned_network_or_cache",
            "offline_verified": False,
            "evidence": "tracked source-level dependency declarations and toolchain pins; resolved bytes remain runner-local",
            "gradle_distribution": gradle_distribution,
        },
        "toolchain": toolchain,
        "go_modules": [{
            "module": pins["mobile_module"],
            "version": pins["mobile_version"],
            "commands": ["go build -buildmode=c-shared"],
        }],
        "spec": {
            "root": "source",
            "path": spec_relative,
            "sha256": spec_sha256,
            "size_bytes": spec_file.stat().st_size,
        },
        "inputs": inputs,
    }


def verify_manifest(
    source_root: Path,
    source_commit: str,
    source_tree: str,
    spec_path: Path,
    manifest_path: Path,
    *,
    java_version: str | None = None,
    gradle_archive: Path | None = None,
    gradle_root: Path | None = None,
    go_build_origin: str = "source_tree",
    go_binary_sha256: str | None = None,
) -> None:
    try:
        actual = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency provenance manifest is not valid JSON") from error
    if actual != create_manifest(
        source_root,
        source_commit,
        source_tree,
        spec_path,
        java_version=java_version,
        gradle_archive=gradle_archive,
        gradle_root=gradle_root,
        go_build_origin=go_build_origin,
        go_binary_sha256=go_binary_sha256,
    ):
        raise ValueError("dependency provenance or declared input hashes changed after the build")


def _write_json(path: Path, document: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", type=Path)
    parser.add_argument("--source-commit")
    parser.add_argument("--source-tree")
    parser.add_argument("--spec", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--verify-manifest", action="store_true")
    parser.add_argument("--java-version")
    parser.add_argument("--go-build-origin", choices=("source_tree", "binary"), default="source_tree")
    parser.add_argument("--go-binary-sha256")
    parser.add_argument("--gradle-archive", type=Path)
    parser.add_argument("--gradle-root", type=Path)
    parser.add_argument("--verify-gradle-distribution", action="store_true")
    parser.add_argument("--print-pin", choices=sorted(_DEFAULT_PINS))
    args = parser.parse_args(argv)
    try:
        if args.spec is not None:
            source_root = args.source_root or _infer_source_root(args.spec)
            pins = dependency_pins(source_root, args.spec)
        else:
            source_root = args.source_root or REPOSITORY_ROOT
            pins = dependency_pins(source_root)
        if args.print_pin:
            print(pins[args.print_pin])
            return 0
        if args.verify_gradle_distribution:
            if args.gradle_archive is None or args.gradle_root is None:
                parser.error("--gradle-archive and --gradle-root are required for distribution verification")
            print(json.dumps(
                _verify_external_gradle_distribution(args.gradle_archive, args.gradle_root, pins),
                sort_keys=True,
            ))
            return 0
        if not args.source_root or not args.source_commit or not args.source_tree or not args.spec:
            parser.error("--source-root, --source-commit, --source-tree, and --spec are required")
        java_version = args.java_version
        if args.verify_manifest:
            if not args.manifest:
                parser.error("--manifest is required with --verify-manifest")
            verify_manifest(
                args.source_root,
                args.source_commit,
                args.source_tree,
                args.spec,
                args.manifest,
                java_version=java_version,
                gradle_archive=args.gradle_archive,
                gradle_root=args.gradle_root,
                go_build_origin=args.go_build_origin,
                go_binary_sha256=args.go_binary_sha256,
            )
            print("android dependency provenance verification passed")
            return 0
        if not args.output:
            parser.error("--output is required unless verifying a manifest")
        _write_json(
            args.output,
            create_manifest(
                args.source_root,
                args.source_commit,
                args.source_tree,
                args.spec,
                java_version=java_version,
                gradle_archive=args.gradle_archive,
                gradle_root=args.gradle_root,
                go_build_origin=args.go_build_origin,
                go_binary_sha256=args.go_binary_sha256,
            ),
        )
    except (OSError, ValueError) as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
