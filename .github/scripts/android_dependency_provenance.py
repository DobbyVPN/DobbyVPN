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
CLOSURE_KIND = "dobbyvpn.android.dependency-closure"
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
    if not isinstance(value, str) or not value or "\x00" in value or "\n" in value or "\\" in value:
        raise ValueError(f"{label} must be a canonical relative path")
    path = Path(value)
    if path.is_absolute() or path.as_posix() != value or any(part in {"", ".", ".."} for part in path.parts):
        raise ValueError(f"{label} must be a canonical relative path")
    return path


def _source_relative(source_root: Path, path: Path, label: str) -> Path:
    """Return a canonical source-relative path without following links."""

    if not path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
        raise ValueError(f"{label} must be an absolute path without dot components")
    root = source_root.resolve(strict=True)
    try:
        relative = path.relative_to(root)
    except ValueError as error:
        raise ValueError(f"{label} must be beneath the exact source root") from error
    if not relative.parts:
        raise ValueError(f"{label} must be beneath a dedicated source directory")
    current = root
    for part in relative.parts:
        current /= part
        try:
            if stat.S_ISLNK(current.lstat().st_mode):
                raise ValueError(f"{label} must not traverse a symlink")
        except FileNotFoundError:
            # The allowlist is also used while a caller is materializing a
            # closure. Missing leaf bytes are checked by _closure_evidence.
            break
    return relative


def closure_staging_paths(source_root: Path, closure_path: Path) -> tuple[str, ...]:
    """Return the exact owner-staged files permitted in a clean checkout."""

    root = source_root.resolve(strict=True)
    closure_relative = _source_relative(root, closure_path, "dependency closure")
    if len(closure_relative.parts) < 2:
        raise ValueError("dependency closure must be inside a dedicated source directory")
    info = closure_path.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise ValueError("dependency closure evidence must be a regular file")
    try:
        document = _strict_json_loads(closure_path.read_text(encoding="utf-8"), "dependency closure evidence")
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency closure evidence is not valid JSON") from error
    if not isinstance(document, dict) or document.get("schema") != SCHEMA or document.get("kind") != CLOSURE_KIND:
        raise ValueError("dependency closure evidence has an unsupported schema")
    closure_directory = root / closure_relative.parent
    directory_info = closure_directory.lstat()
    if stat.S_ISLNK(directory_info.st_mode) or not stat.S_ISDIR(directory_info.st_mode):
        raise ValueError("dependency closure directory must be a real directory")
    allowed: set[str] = set()

    def add_declared(path: Path, label: str) -> None:
        relative = _source_relative(root, path, label)
        try:
            relative.relative_to(closure_relative.parent)
        except ValueError as error:
            raise ValueError(f"{label} must remain beneath the dedicated closure directory") from error
        key = relative.as_posix()
        if key in allowed:
            raise ValueError(f"dependency closure declares the same path twice: {key}")
        try:
            item = path.lstat()
        except FileNotFoundError:
            item = None
        except OSError as error:
            raise ValueError(f"{label} is unavailable") from error
        if item is not None and (stat.S_ISLNK(item.st_mode) or not stat.S_ISREG(item.st_mode)):
            raise ValueError(f"{label} must be a regular non-symlink file")
        allowed.add(key)

    add_declared(closure_path, "dependency closure")
    artifact_root_relative = _safe_relative(document.get("artifact_root"), "dependency artifact_root")
    artifact_root = closure_directory / artifact_root_relative
    artifact_root_info = artifact_root.lstat()
    if stat.S_ISLNK(artifact_root_info.st_mode) or not stat.S_ISDIR(artifact_root_info.st_mode):
        raise ValueError("dependency artifact_root must be a real directory")
    artifacts = document.get("resolved_artifacts")
    if not isinstance(artifacts, list) or not artifacts:
        raise ValueError("dependency closure evidence must enumerate resolved artifacts")
    for item in artifacts:
        if not isinstance(item, dict):
            raise ValueError("resolved dependency entries must be objects")
        add_declared(artifact_root / _safe_relative(item.get("path"), "resolved dependency artifact"), "resolved dependency artifact")

    cache_root_relative = _safe_relative(document.get("cache_root"), "dependency cache_root")
    cache_root = closure_directory / cache_root_relative
    cache_info = cache_root.lstat()
    if stat.S_ISLNK(cache_info.st_mode) or not stat.S_ISDIR(cache_info.st_mode):
        raise ValueError("dependency cache_root must be a real directory")
    cache_entries = document.get("cache_entries")
    if not isinstance(cache_entries, list) or not cache_entries:
        raise ValueError("dependency closure evidence must enumerate Gradle cache metadata")
    for item in cache_entries:
        if not isinstance(item, dict):
            raise ValueError("Gradle cache entries must be objects")
        add_declared(cache_root / _safe_relative(item.get("path"), "Gradle cache entry"), "Gradle cache entry")

    for field, label in (("resolution_evidence", "dependency resolver log"), ("verification_metadata", "dependency verification metadata")):
        record = document.get(field)
        if not isinstance(record, dict):
            raise ValueError(f"{label} declaration is missing")
        add_declared(closure_directory / _safe_relative(record.get("path"), f"{label}.path"), label)
    return tuple(sorted(allowed))


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


def _evidence_file(base: Path, relative: object, label: str) -> tuple[Path, str, int, str]:
    path, digest, size = _regular_file_beneath(base, relative, label)
    canonical = _safe_relative(relative, f"{label}.path").as_posix()
    return path, digest, size, canonical


def _closure_evidence(path: Path, source_commit: str, source_tree: str) -> dict[str, object]:
    """Validate every byte in an owner-captured Android dependency closure."""

    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise ValueError("dependency closure evidence must be a regular file")
    try:
        document = _strict_json_loads(path.read_text(encoding="utf-8"), "dependency closure evidence")
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency closure evidence is not valid JSON") from error
    if not isinstance(document, dict) or document.get("schema") != SCHEMA or document.get("kind") != CLOSURE_KIND:
        raise ValueError("dependency closure evidence has an unsupported schema")
    if document.get("source_commit") != source_commit or document.get("source_tree") != source_tree:
        raise ValueError("dependency closure evidence is not bound to this source tree")
    mode = document.get("mode")
    offline = document.get("offline_verified")
    if mode not in {"offline", "pinned_network_or_cache"} or not isinstance(offline, bool):
        raise ValueError("dependency closure evidence has an invalid resolution mode")
    if offline and mode != "offline":
        raise ValueError("offline dependency evidence must use offline mode")

    artifact_root_relative = _safe_relative(document.get("artifact_root"), "dependency artifact_root")
    artifact_root = path.parent / artifact_root_relative
    artifacts = document.get("resolved_artifacts")
    if not isinstance(artifacts, list) or not artifacts:
        raise ValueError("dependency closure evidence must enumerate resolved artifacts")
    normalized_artifacts: list[dict[str, object]] = []
    seen_coordinates: set[str] = set()
    seen_urls: set[str] = set()
    seen_paths: set[str] = set()
    for item in artifacts:
        if not isinstance(item, dict):
            raise ValueError("resolved dependency entries must be objects")
        coordinate = item.get("coordinate")
        url = item.get("url")
        relative_artifact = item.get("path")
        digest = item.get("sha256")
        size = item.get("size_bytes")
        if (
            not isinstance(coordinate, str) or not coordinate or "\n" in coordinate or coordinate in seen_coordinates
            or not isinstance(url, str) or not url.startswith("https://") or url != url.strip() or url in seen_urls
            or not isinstance(relative_artifact, str) or not SHA256.fullmatch(str(digest))
            or not isinstance(size, int) or isinstance(size, bool) or size <= 0
        ):
            raise ValueError("resolved dependency entries must bind unique HTTPS URL, path, SHA-256, and size")
        _artifact_path, observed_digest, observed_size, canonical_path = _evidence_file(
            artifact_root, relative_artifact, "resolved dependency artifact"
        )
        if canonical_path in seen_paths or observed_digest != digest or observed_size != size:
            raise ValueError(f"resolved dependency artifact bytes do not match their advertised identity: {canonical_path}")
        seen_coordinates.add(coordinate)
        seen_urls.add(url)
        seen_paths.add(canonical_path)
        normalized_artifacts.append({
            "coordinate": coordinate,
            "url": url,
            "path": canonical_path,
            "sha256": digest,
            "size_bytes": size,
        })
    normalized_artifacts.sort(key=lambda item: (str(item["coordinate"]), str(item["url"])))

    cache_root_relative = _safe_relative(document.get("cache_root"), "dependency cache_root")
    cache_root = path.parent / cache_root_relative
    cache_entries = document.get("cache_entries")
    if not isinstance(cache_entries, list) or not cache_entries:
        raise ValueError("dependency closure evidence must enumerate Gradle cache metadata")
    normalized_cache: list[dict[str, object]] = []
    seen_cache_paths: set[str] = set()
    for item in cache_entries:
        if not isinstance(item, dict):
            raise ValueError("Gradle cache entries must be objects")
        relative_cache = item.get("path")
        digest = item.get("sha256")
        size = item.get("size_bytes")
        if not isinstance(relative_cache, str) or not SHA256.fullmatch(str(digest)) or not isinstance(size, int) or isinstance(size, bool) or size < 0:
            raise ValueError("Gradle cache entries must bind path, SHA-256, and size")
        _cache_path, observed_digest, observed_size, canonical_path = _evidence_file(
            cache_root, relative_cache, "Gradle cache entry"
        )
        if canonical_path in seen_cache_paths or observed_digest != digest or observed_size != size:
            raise ValueError(f"Gradle cache bytes do not match their advertised identity: {canonical_path}")
        seen_cache_paths.add(canonical_path)
        normalized_cache.append({"path": canonical_path, "sha256": digest, "size_bytes": size})
    normalized_cache.sort(key=lambda item: str(item["path"]))

    def evidence_record(field: str, label: str) -> dict[str, object]:
        record = document.get(field)
        if not isinstance(record, dict):
            raise ValueError(f"{label} declaration is missing")
        digest = record.get("sha256")
        size = record.get("size_bytes")
        if not isinstance(digest, str) or not SHA256.fullmatch(digest) or not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise ValueError(f"{label} identity is invalid")
        _path, observed_digest, observed_size, canonical_path = _evidence_file(
            path.parent, record.get("path"), label
        )
        if observed_digest != digest or observed_size != size:
            raise ValueError(f"{label} bytes do not match their advertised identity")
        return {"path": canonical_path, "sha256": digest, "size_bytes": size}

    resolver = evidence_record("resolution_evidence", "dependency resolver log")
    verification = document.get("verification_metadata")
    if not isinstance(verification, dict) or verification.get("status") != "present":
        raise ValueError("dependency evidence requires byte-bound verification metadata")
    verification_identity = evidence_record("verification_metadata", "dependency verification metadata")
    if resolver["path"] == verification_identity["path"]:
        raise ValueError("dependency resolver log and verification metadata must be distinct files")
    return {
        "path": path.name,
        "sha256": _sha256(path),
        "size_bytes": info.st_size,
        "mode": mode,
        "offline_verified": offline,
        "artifact_root": artifact_root_relative.as_posix(),
        "resolved_artifacts": normalized_artifacts,
        "cache_root": cache_root_relative.as_posix(),
        "cache_entries": normalized_cache,
        "resolution_evidence": resolver,
        "verification_metadata": {"status": "present", **verification_identity},
    }


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


def create_closure_manifest(
    source_root: Path, source_commit: str, source_tree: str, closure_path: Path
) -> dict[str, object]:
    if not SHA40.fullmatch(source_commit) or not SHA40.fullmatch(source_tree):
        raise ValueError("source commit/tree must be full lowercase Git identities")
    source_root = source_root.absolute()
    _source_go_version(source_root)
    inputs: list[dict[str, object]] = []
    for relative in DECLARED_INPUTS:
        _path, digest, size = _regular_file_beneath(source_root, relative, "dependency input")
        inputs.append({"path": relative, "sha256": digest, "size_bytes": size})
    wrapper = _wrapper_values(source_root / "kmp_module/gradle/wrapper/gradle-wrapper.properties")
    closure = _closure_evidence(closure_path, source_commit, source_tree)
    try:
        closure_relative = closure_path.resolve(strict=True).relative_to(source_root.resolve(strict=True)).as_posix()
    except (OSError, ValueError) as error:
        raise ValueError("dependency closure must be beneath the exact source root") from error
    closure_inputs = [
        {"path": f"dependency-closure/{closure_relative}", "sha256": closure["sha256"], "size_bytes": closure["size_bytes"]},
        {"path": f"dependency-closure/{closure['resolution_evidence']['path']}", "sha256": closure["resolution_evidence"]["sha256"], "size_bytes": closure["resolution_evidence"]["size_bytes"]},
        {"path": f"dependency-closure/{closure['verification_metadata']['path']}", "sha256": closure["verification_metadata"]["sha256"], "size_bytes": closure["verification_metadata"]["size_bytes"]},
    ]
    closure_inputs.extend(
        {
            "path": f"dependency-closure/{closure['artifact_root']}/{item['path']}",
            "sha256": item["sha256"],
            "size_bytes": item["size_bytes"],
        }
        for item in closure["resolved_artifacts"]
    )
    closure_inputs.extend(
        {
            "path": f"dependency-closure/{closure['cache_root']}/{item['path']}",
            "sha256": item["sha256"],
            "size_bytes": item["size_bytes"],
        }
        for item in closure["cache_entries"]
    )
    inputs.extend(closure_inputs)
    return {
        "schema": SCHEMA,
        "kind": "dobbyvpn.android.dependency-provenance",
        "repository": "DobbyVPN/DobbyVPN",
        "source": {"commit": source_commit, "tree": source_tree},
        "closure": {
            "path": closure_relative,
            "sha256": closure["sha256"],
            "size_bytes": closure["size_bytes"],
            "artifact_root": closure["artifact_root"],
            "cache_root": closure["cache_root"],
        },
        "dependency_provenance": "complete_owner_evidence",
        "resolution": {
            "mode": closure["mode"],
            "offline_verified": closure["offline_verified"],
            "evidence": "owner-captured-resolved-artifact-digests-and-complete-gradle-cache-metadata",
            "cache_policy": "Only the byte-addressed artifacts and Gradle module cache entries enumerated in the owner closure are admissible.",
            "repositories": REPOSITORIES,
            "gradle_distribution": wrapper,
            "artifact_root": closure["artifact_root"],
            "resolved_artifacts": closure["resolved_artifacts"],
            "cache_root": closure["cache_root"],
            "cache_entries": closure["cache_entries"],
            "resolution_evidence": closure["resolution_evidence"],
            "verification_metadata": closure["verification_metadata"],
        },
        "toolchain": {
            "java_major": JAVA_MAJOR,
            "android_build_tools": ANDROID_BUILD_TOOLS,
            "android_ndk": ANDROID_NDK,
            "go_version": GO_VERSION,
            "go_source_commit": GO_SOURCE_COMMIT,
        },
        "go_modules": [{"module": MOBILE_MODULE, "version": MOBILE_VERSION, "commands": ["gomobile", "gobind"]}],
        "inputs": inputs,
    }


def verify_closure_manifest(
    source_root: Path, source_commit: str, source_tree: str, closure_path: Path, manifest_path: Path
) -> None:
    try:
        info = manifest_path.lstat()
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise ValueError("dependency provenance manifest must be a regular file")
        actual = _strict_json_loads(manifest_path.read_text(encoding="utf-8"), "dependency provenance manifest")
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency provenance manifest is not valid JSON") from error
    if actual != create_closure_manifest(source_root, source_commit, source_tree, closure_path):
        raise ValueError("dependency provenance or closure input hashes changed after the build")


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
    parser.add_argument("--spec", type=Path)
    parser.add_argument("--closure-evidence", type=Path)
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
    parser.add_argument("--print-staged-paths", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.print_staged_paths:
            if not args.source_root or not args.closure_evidence:
                parser.error("--source-root and --closure-evidence are required with --print-staged-paths")
            for staged in closure_staging_paths(args.source_root, args.closure_evidence):
                print(staged)
            return 0
        if bool(args.spec) == bool(args.closure_evidence):
            parser.error("exactly one of --spec or --closure-evidence is required")
        if args.print_mobile_version:
            if args.spec is None:
                print(f"{MOBILE_MODULE}@{MOBILE_VERSION}")
                return 0
            _read_spec(args.spec)
            print(f"{MOBILE_MODULE}@{MOBILE_VERSION}")
            return 0
        if args.print_go_source_commit:
            if args.spec is None:
                print(GO_SOURCE_COMMIT)
                return 0
            _read_spec(args.spec)
            print(GO_SOURCE_COMMIT)
            return 0
        if args.print_go_version:
            if args.spec is None:
                print(GO_VERSION)
                return 0
            _read_spec(args.spec)
            print(GO_VERSION)
            return 0
        if args.print_gradle_url:
            if args.spec is None:
                print(GRADLE_URL)
                return 0
            _read_spec(args.spec)
            print(GRADLE_URL)
            return 0
        if args.print_gradle_sha256:
            if args.spec is None:
                print(GRADLE_SHA256)
                return 0
            _read_spec(args.spec)
            print(GRADLE_SHA256)
            return 0
        if args.verify_gradle_distribution:
            if args.spec is None:
                parser.error("--spec is required for distribution verification")
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
            if args.closure_evidence is not None:
                verify_closure_manifest(
                    args.source_root, args.source_commit, args.source_tree,
                    args.closure_evidence, args.manifest,
                )
                print("android dependency provenance verification passed")
                return 0
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
        if args.closure_evidence is not None:
            _write_json(
                args.output,
                create_closure_manifest(
                    args.source_root, args.source_commit, args.source_tree, args.closure_evidence,
                ),
            )
            return 0
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
