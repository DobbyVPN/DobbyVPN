#!/usr/bin/env python3
"""Validate the tracked Android dependency pins and write provenance.

The public checkout intentionally records source-level dependency declarations,
not a large cache of Maven/Gradle bytes. Resolution may use the network or an
owner-provided cache, but the declared inputs and toolchain pins are checked
before and after the build. This keeps the source checkout clean so the driver
can bind the produced APK to one exact Git tree without claiming a complete
offline dependency closure.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import stat
from typing import Any
from pathlib import PurePosixPath
import zipfile


SHA256 = re.compile(r"^[0-9a-f]{64}$")
SHA40 = re.compile(r"^[0-9a-f]{40}$")
SCHEMA = 1
KIND = "dobbyvpn.android.dependency-spec"
MOBILE_MODULE = "golang.org/x/mobile"
MOBILE_VERSION = "v0.0.0-20260520154334-0e4426e1883d"
GO_VERSION = "1.25.1"
GO_SOURCE_COMMIT = "56ebf80e57db9f61981fc0636fc6419dc6f68eda"
GRADLE_URL = "https://services.gradle.org/distributions/gradle-8.13-bin.zip"
GRADLE_SHA256 = "20f1b1176237254a6fc204d8434196fa11a4cfb387567519c61556e8710aed78"
GRADLE_VERSION = "8.13"
JAVA_MAJOR = 17
ANDROID_BUILD_TOOLS = "36.0.0"
ANDROID_NDK = "27.3.13750724"
REPOSITORIES = [
    {"id": "google", "url": "https://dl.google.com/dl/android/maven2/"},
    {"id": "mavenCentral", "url": "https://repo.maven.apache.org/maven2/"},
    {"id": "gradlePluginPortal", "url": "https://plugins.gradle.org/m2/"},
]
DECLARED_INPUTS = (
    ".go-version",
    "kmp_module/settings.gradle.kts",
    "kmp_module/build.gradle.kts",
    "kmp_module/app/build.gradle.kts",
    "kmp_module/grpcprotos/build.gradle.kts",
    "kmp_module/grpcstub/build.gradle.kts",
    "kmp_module/gradle/libs.versions.toml",
    "kmp_module/gradle/wrapper/gradle-wrapper.properties",
    "kmp_module/gradle/wrapper/gradle-wrapper.jar",
    "kmp_module/gradle.properties",
    "go_module/go.mod",
    "go_module/go.sum",
)


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _strict_json_loads(raw: str, label: str) -> object:
    def reject_duplicates(pairs: list[tuple[str, object]]) -> dict[str, object]:
        value: dict[str, object] = {}
        for key, item in pairs:
            if key in value:
                raise ValueError(f"{label} contains a duplicate JSON key")
            value[key] = item
        return value

    def reject_non_finite(token: str) -> object:
        raise ValueError(f"{label} contains a non-finite JSON number: {token}")

    def reject_float_overflow(token: str) -> float:
        value = float(token)
        if not math.isfinite(value):
            raise ValueError(f"{label} contains a non-finite JSON number: {token}")
        return value

    return json.loads(
        raw,
        object_pairs_hook=reject_duplicates,
        parse_constant=reject_non_finite,
        parse_float=reject_float_overflow,
    )


def _safe_relative(value: object, label: str) -> Path:
    if not isinstance(value, str) or not value or "\x00" in value or "\\" in value:
        raise ValueError(f"{label} must be a canonical relative path")
    path = Path(value)
    if path.is_absolute() or path.as_posix() != value or any(part in {"", ".", ".."} for part in path.parts):
        raise ValueError(f"{label} must be a canonical relative path")
    return path


def _regular_file_beneath(root: Path, relative: object, label: str) -> tuple[Path, str, int]:
    relative_path = _safe_relative(relative, f"{label}.path")
    try:
        root_info = root.lstat()
        root_real = root.resolve(strict=True)
    except OSError as error:
        raise ValueError(f"{label} root is unavailable") from error
    if stat.S_ISLNK(root_info.st_mode) or not stat.S_ISDIR(root_info.st_mode):
        raise ValueError(f"{label} root must be a real directory")
    path = root / relative_path
    try:
        current = root
        for part in relative_path.parts:
            current /= part
            if stat.S_ISLNK(current.lstat().st_mode):
                raise ValueError(f"{label} must not traverse a symlink")
        info = path.lstat()
        path_real = path.resolve(strict=True)
    except OSError as error:
        raise ValueError(f"{label} is unavailable") from error
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise ValueError(f"{label} must be a regular non-symlink file")
    try:
        path_real.relative_to(root_real)
    except ValueError as error:
        raise ValueError(f"{label} escapes its source root") from error
    return path, _sha256(path), info.st_size


def _read_spec(path: Path) -> dict[str, Any]:
    try:
        info = path.lstat()
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise ValueError("dependency specification must be a regular file")
        document = _strict_json_loads(path.read_text(encoding="utf-8"), "dependency specification")
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency specification is not valid JSON") from error
    if not isinstance(document, dict):
        raise ValueError("dependency specification must be an object")
    expected_keys = {"schema", "kind", "repositories", "gradle", "java", "android", "go", "go_mobile"}
    if set(document) != expected_keys:
        raise ValueError("dependency specification has unexpected or missing fields")
    if document["schema"] != SCHEMA or document["kind"] != KIND:
        raise ValueError("dependency specification has an unsupported schema")
    repositories = document["repositories"]
    if repositories != REPOSITORIES:
        raise ValueError("dependency repositories are not the approved repository set")
    gradle = document["gradle"]
    if not isinstance(gradle, dict) or set(gradle) != {"distribution_url", "distribution_sha256"}:
        raise ValueError("dependency Gradle pin is incomplete")
    if gradle["distribution_url"] != GRADLE_URL or gradle["distribution_sha256"] != GRADLE_SHA256:
        raise ValueError("dependency Gradle pin is not the approved immutable distribution")
    if not SHA256.fullmatch(str(gradle["distribution_sha256"])):
        raise ValueError("dependency Gradle SHA-256 is invalid")
    java = document["java"]
    android = document["android"]
    go = document["go"]
    mobile = document["go_mobile"]
    if not isinstance(java, dict) or set(java) != {"major"} or java["major"] != JAVA_MAJOR:
        raise ValueError("Java pin is not the approved major version")
    if not isinstance(android, dict) or set(android) != {"build_tools", "ndk"}:
        raise ValueError("Android toolchain pin is incomplete")
    if android != {"build_tools": ANDROID_BUILD_TOOLS, "ndk": ANDROID_NDK}:
        raise ValueError("Android toolchain pin is not the approved exact set")
    if (
        not isinstance(go, dict)
        or set(go) != {"version", "source_commit"}
        or go["version"] != GO_VERSION
        or go["source_commit"] != GO_SOURCE_COMMIT
    ):
        raise ValueError("Go pin is not the approved version")
    if not isinstance(mobile, dict) or set(mobile) != {"module", "version"}:
        raise ValueError("x/mobile pin is incomplete")
    if mobile != {"module": MOBILE_MODULE, "version": MOBILE_VERSION}:
        raise ValueError("x/mobile pin is not the approved immutable revision")
    return document


def _zip_relative_name(name: str, label: str) -> str:
    if not isinstance(name, str) or not name or "\x00" in name or "\\" in name:
        raise ValueError(f"{label} contains an unsafe archive path: {name!r}")
    trimmed = name[:-1] if name.endswith("/") else name
    if not trimmed or any(part in {"", ".", ".."} for part in trimmed.split("/")):
        raise ValueError(f"{label} contains an unsafe archive path: {name!r}")
    path = PurePosixPath(trimmed)
    if path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
        raise ValueError(f"{label} contains an unsafe archive path: {name!r}")
    return path.as_posix()


def _gradle_root_tree(root: Path) -> tuple[dict[str, tuple[str, int]], str]:
    root = root.absolute()
    current = Path(root.anchor)
    for part in root.parts[1:]:
        current /= part
        try:
            component = current.lstat()
        except OSError as error:
            raise ValueError("external Gradle root path is unavailable") from error
        if stat.S_ISLNK(component.st_mode):
            raise ValueError("external Gradle root path must not traverse a symlink")
    try:
        info = root.lstat()
    except OSError as error:
        raise ValueError("external Gradle root is unavailable") from error
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        raise ValueError("external Gradle root must be a real directory")
    if root.name != f"gradle-{GRADLE_VERSION}":
        raise ValueError("external Gradle root has the wrong version directory name")
    files: dict[str, tuple[str, int]] = {}
    try:
        paths = sorted(root.rglob("*"), key=lambda item: item.relative_to(root).as_posix())
    except OSError as error:
        raise ValueError("external Gradle root cannot be enumerated") from error
    for path in paths:
        relative = path.relative_to(root).as_posix()
        try:
            item_info = path.lstat()
        except OSError as error:
            raise ValueError(f"external Gradle root entry is unavailable: {relative}") from error
        if stat.S_ISLNK(item_info.st_mode):
            raise ValueError(f"external Gradle root contains a symlink: {relative}")
        if stat.S_ISDIR(item_info.st_mode):
            continue
        if not stat.S_ISREG(item_info.st_mode):
            raise ValueError(f"external Gradle root contains a non-regular entry: {relative}")
        files[relative] = (_sha256(path), item_info.st_size)
    encoded = (json.dumps(
        [{"bytes": size, "path": path, "sha256": digest} for path, (digest, size) in sorted(files.items())],
        sort_keys=True,
        separators=(",", ":"),
    ) + "\n").encode("utf-8")
    return files, hashlib.sha256(encoded).hexdigest()


def _verify_external_gradle_distribution(archive: Path, root: Path) -> dict[str, object]:
    archive = archive.absolute()
    current = Path(archive.anchor)
    for part in archive.parts[1:]:
        current /= part
        try:
            component = current.lstat()
        except OSError as error:
            raise ValueError("external Gradle archive path is unavailable") from error
        if stat.S_ISLNK(component.st_mode):
            raise ValueError("external Gradle archive path must not traverse a symlink")
    try:
        archive_info = archive.lstat()
    except OSError as error:
        raise ValueError("external Gradle archive is unavailable") from error
    if stat.S_ISLNK(archive_info.st_mode) or not stat.S_ISREG(archive_info.st_mode):
        raise ValueError("external Gradle archive must be a regular non-symlink file")
    archive_sha256 = _sha256(archive)
    if archive_sha256 != GRADLE_SHA256:
        raise ValueError("external Gradle archive SHA-256 does not match the trusted spec")
    expected_prefix = f"gradle-{GRADLE_VERSION}/"
    archive_files: dict[str, tuple[str, int]] = {}
    archive_directories: set[str] = set()
    try:
        with zipfile.ZipFile(archive) as zipped:
            for item in zipped.infolist():
                name = _zip_relative_name(item.filename, "external Gradle archive")
                mode = (item.external_attr >> 16) & 0xFFFF
                if stat.S_ISLNK(mode):
                    raise ValueError("external Gradle archive contains a symlink")
                if item.is_dir() or item.filename.endswith("/"):
                    if name != expected_prefix[:-1] and not name.startswith(expected_prefix):
                        raise ValueError("external Gradle archive contains an unexpected top-level directory")
                    directory_relative = (
                        "" if name == expected_prefix[:-1] else name[len(expected_prefix):]
                    )
                    if directory_relative in archive_files:
                        raise ValueError(
                            f"external Gradle archive has a file/directory path conflict: {directory_relative}"
                        )
                    if name in archive_directories:
                        raise ValueError(f"external Gradle archive contains a duplicate directory: {name}")
                    archive_directories.add(name)
                    continue
                if not name.startswith(expected_prefix):
                    raise ValueError("external Gradle archive contains an unexpected top-level path")
                relative = name[len(expected_prefix):]
                if not relative:
                    raise ValueError("external Gradle archive contains a malformed root file entry")
                if relative in archive_files:
                    raise ValueError(f"external Gradle archive contains a duplicate path: {relative}")
                if relative in archive_directories:
                    raise ValueError(f"external Gradle archive has a file/directory path conflict: {relative}")
                digest = hashlib.sha256()
                size = 0
                with zipped.open(item) as stream:
                    for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                        size += len(chunk)
                        digest.update(chunk)
                archive_files[relative] = (digest.hexdigest(), size)
    except (OSError, zipfile.BadZipFile) as error:
        raise ValueError("external Gradle archive is not a readable ZIP") from error
    root_files, root_tree_sha256 = _gradle_root_tree(root)
    if set(archive_files) != set(root_files):
        raise ValueError("external Gradle root contents do not match the verified archive")
    for relative, expected in archive_files.items():
        if root_files[relative] != expected:
            raise ValueError(f"external Gradle root bytes do not match archive: {relative}")
    gradle_entry = root / "bin/gradle"
    if "bin/gradle" not in root_files:
        raise ValueError("external Gradle root does not contain bin/gradle")
    if not (gradle_entry.stat().st_mode & stat.S_IXUSR):
        raise ValueError("external Gradle root bin/gradle is not executable")
    return {
        "archive_path": str(archive.absolute()),
        "archive_sha256": archive_sha256,
        "archive_size_bytes": archive_info.st_size,
        "root_path": str(root.absolute()),
        "root_tree_sha256": root_tree_sha256,
        "sha256": GRADLE_SHA256,
        "source": "external_verified_archive",
        "url": GRADLE_URL,
        "version": GRADLE_VERSION,
    }


def _wrapper_values(
    path: Path,
    *,
    gradle_archive: Path | None = None,
    gradle_root: Path | None = None,
) -> dict[str, object]:
    values: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        if key in values:
            raise ValueError(f"Gradle wrapper properties contain a duplicate key: {key}")
        values[key] = value.replace("\\:", ":")
    if values.get("distributionUrl") != GRADLE_URL:
        raise ValueError("Gradle wrapper distribution is not the pinned 8.13 binary")
    wrapper_sha256 = values.get("distributionSha256Sum", "").lower()
    if wrapper_sha256 and wrapper_sha256 != GRADLE_SHA256:
        raise ValueError("Gradle wrapper distribution SHA-256 does not match the tracked pin")
    if not wrapper_sha256 and (gradle_archive is None or gradle_root is None):
        raise ValueError(
            "Gradle wrapper checksum is missing; an externally verified Gradle distribution proof is required"
        )
    if gradle_archive is not None or gradle_root is not None:
        if gradle_archive is None or gradle_root is None:
            raise ValueError("external Gradle archive and root proof must be supplied together")
        return _verify_external_gradle_distribution(gradle_archive, gradle_root)
    return {"source": "wrapper_checksum", "url": GRADLE_URL, "sha256": GRADLE_SHA256, "version": GRADLE_VERSION}


def _spec_location(
    source_root: Path,
    spec_path: Path,
    trusted_helper_root: Path | None,
    trusted_helper_sha: str | None,
) -> tuple[Path, str, str, str | None]:
    if trusted_helper_root is None:
        try:
            spec_relative = spec_path.absolute().relative_to(source_root)
        except ValueError as error:
            raise ValueError("dependency specification must be beneath the exact source root") from error
        spec_root = source_root
        spec_root_kind = "source"
        trusted_commit = None
    else:
        if not SHA40.fullmatch(trusted_helper_sha or ""):
            raise ValueError("trusted helper commit must be a full lowercase Git identity")
        trusted_helper_root = trusted_helper_root.absolute()
        try:
            spec_relative = spec_path.absolute().relative_to(trusted_helper_root)
        except ValueError as error:
            raise ValueError("trusted dependency specification must be beneath the trusted helper root") from error
        spec_root = trusted_helper_root
        spec_root_kind = "trusted_helper"
        trusted_commit = trusted_helper_sha
    spec_relative_text = spec_relative.as_posix()
    spec_file, spec_sha256, spec_size = _regular_file_beneath(
        spec_root, spec_relative_text, "dependency specification"
    )
    return spec_file, spec_root_kind, spec_relative_text, trusted_commit


def _source_go_version(source_root: Path) -> str:
    try:
        version = (source_root / ".go-version").read_text(encoding="utf-8").strip()
    except (OSError, UnicodeError) as error:
        raise ValueError(".go-version is unavailable") from error
    if version != GO_VERSION:
        raise ValueError(f".go-version must be exactly {GO_VERSION}, got {version!r}")
    return version


def create_manifest(
    source_root: Path,
    source_commit: str,
    source_tree: str,
    spec_path: Path,
    *,
    trusted_helper_root: Path | None = None,
    trusted_helper_sha: str | None = None,
    java_version: str = str(JAVA_MAJOR),
    gradle_archive: Path | None = None,
    gradle_root: Path | None = None,
) -> dict[str, object]:
    if not SHA40.fullmatch(source_commit) or not SHA40.fullmatch(source_tree):
        raise ValueError("source commit/tree must be full lowercase Git identities")
    source_root = source_root.absolute()
    _source_go_version(source_root)
    if not isinstance(java_version, str) or not java_version:
        raise ValueError("observed Java version is required")
    if not (java_version == str(JAVA_MAJOR) or java_version.startswith(f"{JAVA_MAJOR}.")):
        raise ValueError(f"observed Java version must have major {JAVA_MAJOR}, got {java_version!r}")
    spec_file, spec_root_kind, spec_relative_text, trusted_commit = _spec_location(
        source_root, spec_path, trusted_helper_root, trusted_helper_sha
    )
    spec_sha256 = _sha256(spec_file)
    spec_size = spec_file.stat().st_size
    spec = _read_spec(spec_file)
    inputs: list[dict[str, object]] = []
    for relative in DECLARED_INPUTS:
        _path, digest, size = _regular_file_beneath(source_root, relative, "dependency input")
        inputs.append({"path": relative, "sha256": digest, "size_bytes": size})
    gradle_distribution = _wrapper_values(
        source_root / "kmp_module/gradle/wrapper/gradle-wrapper.properties",
        gradle_archive=gradle_archive,
        gradle_root=gradle_root,
    )
    spec_input: dict[str, object] = {
        "path": spec_relative_text,
        "sha256": spec_sha256,
        "size_bytes": spec_size,
    }
    if spec_root_kind != "source":
        spec_input["root"] = spec_root_kind
        spec_input["trusted_commit"] = trusted_commit
    inputs.append(spec_input)
    return {
        "schema": SCHEMA,
        "kind": "dobbyvpn.android.dependency-provenance",
        "repository": "DobbyVPN/DobbyVPN",
        "source": {"commit": source_commit, "tree": source_tree},
        "dependency_provenance": "tracked_dependency_spec",
        "resolution": {
            "mode": "pinned_network_or_cache",
            "offline_verified": False,
            "evidence": (
                "tracked source-level dependency declarations and toolchain pins; "
                "resolved bytes remain runner-local; no complete offline closure is claimed"
            ),
            "repositories": spec["repositories"],
            "gradle_distribution": gradle_distribution,
        },
        "toolchain": {
            "java_major": JAVA_MAJOR,
            "java_version": java_version,
            "android_build_tools": ANDROID_BUILD_TOOLS,
            "android_ndk": ANDROID_NDK,
            "go_version": GO_VERSION,
            "go_source_commit": GO_SOURCE_COMMIT,
        },
        "go_modules": [{"module": MOBILE_MODULE, "version": MOBILE_VERSION, "commands": ["gomobile", "gobind"]}],
        "spec": {
            "root": spec_root_kind,
            "path": spec_relative_text,
            "sha256": spec_sha256,
            "size_bytes": spec_size,
            **({"trusted_commit": trusted_commit} if trusted_commit else {}),
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
    trusted_helper_root: Path | None = None,
    trusted_helper_sha: str | None = None,
    java_version: str = str(JAVA_MAJOR),
    gradle_archive: Path | None = None,
    gradle_root: Path | None = None,
) -> None:
    try:
        info = manifest_path.lstat()
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise ValueError("dependency provenance manifest must be a regular file")
        actual = _strict_json_loads(
            manifest_path.read_text(encoding="utf-8"), "dependency provenance manifest"
        )
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency provenance manifest is not valid JSON") from error
    if not isinstance(actual, dict):
        raise ValueError("dependency provenance manifest must be an object")
    expected = create_manifest(
        source_root,
        source_commit,
        source_tree,
        spec_path,
        trusted_helper_root=trusted_helper_root,
        trusted_helper_sha=trusted_helper_sha,
        java_version=java_version,
        gradle_archive=gradle_archive,
        gradle_root=gradle_root,
    )
    if actual != expected:
        raise ValueError("dependency provenance or declared input hashes changed after the build")


def _write_json(path: Path, document: dict[str, object]) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        json.dump(document, stream, sort_keys=True, separators=(",", ":"))
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    parent_descriptor = os.open(path.parent, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(parent_descriptor)
    finally:
        os.close(parent_descriptor)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", type=Path)
    parser.add_argument("--source-commit")
    parser.add_argument("--source-tree")
    parser.add_argument("--spec", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--verify-manifest", action="store_true")
    parser.add_argument("--trusted-helper-root", type=Path)
    parser.add_argument("--trusted-helper-sha")
    parser.add_argument("--java-version")
    parser.add_argument("--gradle-archive", type=Path)
    parser.add_argument("--gradle-root", type=Path)
    parser.add_argument("--verify-gradle-distribution", action="store_true")
    parser.add_argument("--print-mobile-version", action="store_true")
    parser.add_argument("--print-go-version", action="store_true")
    parser.add_argument("--print-go-source-commit", action="store_true")
    parser.add_argument("--print-gradle-url", action="store_true")
    parser.add_argument("--print-gradle-sha256", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.print_mobile_version:
            _read_spec(args.spec)
            print(f"{MOBILE_MODULE}@{MOBILE_VERSION}")
            return 0
        if args.print_go_source_commit:
            _read_spec(args.spec)
            print(GO_SOURCE_COMMIT)
            return 0
        if args.print_go_version:
            _read_spec(args.spec)
            print(GO_VERSION)
            return 0
        if args.print_gradle_url:
            _read_spec(args.spec)
            print(GRADLE_URL)
            return 0
        if args.print_gradle_sha256:
            _read_spec(args.spec)
            print(GRADLE_SHA256)
            return 0
        if args.verify_gradle_distribution:
            if args.gradle_archive is None or args.gradle_root is None:
                parser.error("--gradle-archive and --gradle-root are required for distribution verification")
            _read_spec(args.spec)
            print(json.dumps(_verify_external_gradle_distribution(args.gradle_archive, args.gradle_root), sort_keys=True))
            return 0
        if not args.source_root or not args.source_commit or not args.source_tree:
            parser.error("--source-root, --source-commit, and --source-tree are required")
        java_version = args.java_version or str(JAVA_MAJOR)
        if args.verify_manifest:
            if not args.manifest:
                parser.error("--manifest is required with --verify-manifest")
            verify_manifest(
                args.source_root,
                args.source_commit,
                args.source_tree,
                args.spec,
                args.manifest,
                trusted_helper_root=args.trusted_helper_root,
                trusted_helper_sha=args.trusted_helper_sha,
                java_version=java_version,
                gradle_archive=args.gradle_archive,
                gradle_root=args.gradle_root,
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
                trusted_helper_root=args.trusted_helper_root,
                trusted_helper_sha=args.trusted_helper_sha,
                java_version=java_version,
                gradle_archive=args.gradle_archive,
                gradle_root=args.gradle_root,
            ),
        )
    except (OSError, ValueError) as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
