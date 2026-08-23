#!/usr/bin/env python3
"""Create deterministic, non-secret Android Gradle/Maven dependency evidence."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import stat


SHA256 = re.compile(r"^[0-9a-f]{64}$")
SCHEMA = 1
KIND = "dobbyvpn.android.dependency-provenance"
PINNED_X_MOBILE = "v0.0.0-20260520154334-0e4426e1883d"
DECLARED_INPUTS = (
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
    """Reject ambiguous JSON before deriving release provenance from it."""

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
    """Resolve one closure byte only when it remains beneath a real root."""
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
            current = current / part
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
        raise ValueError(f"{label} escapes its closure root") from error
    digest = _sha256(path)
    return path, digest, info.st_size


def _evidence_file(base: Path, relative: object, label: str) -> tuple[Path, str, int, str]:
    path, digest, size = _regular_file_beneath(base, relative, label)
    return path, digest, size, _safe_relative(relative, f"{label}.path").as_posix()


def _regular_file(root: Path, relative: str) -> tuple[Path, str, int]:
    # Declared source inputs are part of the same untrusted checkout boundary
    # as resolved artifacts.  Check every ancestor, not only the leaf: a
    # symlinked directory such as ``kmp_module`` could otherwise redirect the
    # digest outside the exact source tree while leaving the final file a
    # regular file.
    path, digest, size = _regular_file_beneath(root, relative, "dependency input")
    return path, digest, size


def _wrapper_values(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        if key in values:
            raise ValueError(f"Gradle wrapper properties contain a duplicate key: {key}")
        values[key] = value.replace("\\:", ":")
    url = values.get("distributionUrl", "")
    checksum = values.get("distributionSha256Sum", "").lower()
    if url != "https://services.gradle.org/distributions/gradle-8.13-bin.zip":
        raise ValueError("Gradle wrapper distribution is not the pinned 8.13 binary")
    if not SHA256.fullmatch(checksum) or checksum == "0" * 64:
        raise ValueError("Gradle wrapper distribution SHA-256 is missing or invalid")
    return {"url": url, "sha256": checksum}


def _closure_evidence(path: Path, source_commit: str, source_tree: str) -> dict[str, object]:
    """Validate an owner-captured, byte-addressed dependency resolution.

    Declared build inputs and repository URLs alone are not a dependency
    closure.  The driver therefore requires this separate evidence document;
    it is produced after the owner's pinned cache/network resolution and
    contains every resolved artifact's digest plus the complete resolver log.
    A missing or merely declarative document fails closed.
    """
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise ValueError("dependency closure evidence must be a regular file")
    try:
        document = _strict_json_loads(path.read_text(encoding="utf-8"), "dependency closure evidence")
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise ValueError("dependency closure evidence is not valid JSON") from error
    if not isinstance(document, dict):
        raise ValueError("dependency closure evidence must be an object")
    if document.get("schema") != 1 or document.get("kind") != "dobbyvpn.android.dependency-closure":
        raise ValueError("dependency closure evidence has an unsupported schema")
    if document.get("source_commit") != source_commit or document.get("source_tree") != source_tree:
        raise ValueError("dependency closure evidence is not bound to this source tree")
    mode = document.get("mode")
    offline = document.get("offline_verified")
    if mode not in {"offline", "pinned_network_or_cache"} or not isinstance(offline, bool):
        raise ValueError("dependency closure evidence has an invalid resolution mode")
    if offline and mode != "offline":
        raise ValueError("offline dependency evidence must use offline mode")
    artifacts = document.get("resolved_artifacts")
    if not isinstance(artifacts, list) or not artifacts:
        raise ValueError("dependency closure evidence must enumerate resolved artifacts")
    artifact_root_relative = _safe_relative(document.get("artifact_root"), "dependency artifact_root")
    artifact_root = path.parent / artifact_root_relative
    normalized: list[dict[str, object]] = []
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
            not isinstance(coordinate, str) or not coordinate or "\n" in coordinate
            or coordinate in seen_coordinates
            or not isinstance(url, str) or not url.startswith("https://") or url != url.strip()
            or url in seen_urls
            or not isinstance(relative_artifact, str)
            or not isinstance(digest, str) or not SHA256.fullmatch(digest)
            or not isinstance(size, int) or isinstance(size, bool) or size <= 0
        ):
            raise ValueError("resolved dependency entries must bind unique HTTPS URL, path, SHA-256, and size")
        _artifact_path, observed_digest, observed_size, canonical_path = _evidence_file(
            artifact_root, relative_artifact, "resolved dependency artifact"
        )
        if canonical_path in seen_paths:
            raise ValueError("resolved dependency entries contain a duplicate artifact path")
        if observed_digest != digest or observed_size != size:
            raise ValueError(f"resolved dependency artifact bytes do not match their advertised identity: {canonical_path}")
        seen_coordinates.add(coordinate)
        seen_urls.add(url)
        seen_paths.add(canonical_path)
        normalized.append({
            "coordinate": coordinate,
            "url": url,
            "path": canonical_path,
            "sha256": digest,
            "size_bytes": size,
        })
    normalized.sort(key=lambda item: (str(item["coordinate"]), str(item["url"])))
    evidence = document.get("resolution_evidence")
    if not isinstance(evidence, dict):
        raise ValueError("dependency closure evidence must name the complete resolver log")
    evidence_path = evidence.get("path")
    evidence_sha = evidence.get("sha256")
    evidence_size = evidence.get("size_bytes")
    if (
        not isinstance(evidence_sha, str) or not SHA256.fullmatch(evidence_sha)
        or not isinstance(evidence_size, int) or isinstance(evidence_size, bool) or evidence_size <= 0
    ):
        raise ValueError("dependency resolver log identity is invalid")
    resolver_log, observed_log_sha, observed_log_size, evidence_path = _evidence_file(
        path.parent, evidence_path, "dependency resolver log"
    )
    if observed_log_sha != evidence_sha or observed_log_size != evidence_size:
        raise ValueError("dependency resolver log bytes do not match its advertised identity")
    verification = document.get("verification_metadata")
    if not isinstance(verification, dict) or verification.get("status") != "present":
        raise ValueError("dependency evidence requires byte-bound verification metadata")
    verification_path = verification.get("path")
    verification_sha = verification.get("sha256")
    verification_size = verification.get("size_bytes")
    if not isinstance(verification_sha, str) or not SHA256.fullmatch(verification_sha) or not isinstance(verification_size, int) or isinstance(verification_size, bool) or verification_size <= 0:
        raise ValueError("dependency verification metadata identity is invalid")
    _verification_file, observed_verification_sha, observed_verification_size, verification_path = _evidence_file(
        path.parent, verification_path, "dependency verification metadata"
    )
    if observed_verification_sha != verification_sha or observed_verification_size != verification_size:
        raise ValueError("dependency verification metadata bytes do not match their advertised identity")
    if evidence_path == verification_path:
        raise ValueError("dependency resolver log and verification metadata must be distinct files")
    closure_sha = _sha256(path)
    return {
        "path": path.name,
        "sha256": closure_sha,
        "size_bytes": info.st_size,
        "mode": mode,
        "offline_verified": offline,
        "artifact_root": artifact_root_relative.as_posix(),
        "resolved_artifacts": normalized,
        "resolution_evidence": {
            "path": evidence_path,
            "sha256": evidence_sha,
            "size_bytes": evidence_size,
        },
        "verification_metadata": {
            **verification,
            "path": verification_path,
            "sha256": verification_sha,
            "size_bytes": verification_size,
        },
    }


def create_manifest(
    source_root: Path, source_commit: str, source_tree: str, closure_path: Path
) -> dict[str, object]:
    if not re.fullmatch(r"[0-9a-f]{40}", source_commit) or not re.fullmatch(r"[0-9a-f]{40}", source_tree):
        raise ValueError("source commit/tree must be full lowercase Git identities")
    records = []
    for relative in DECLARED_INPUTS:
        path, digest, size = _regular_file(source_root, relative)
        records.append({"path": relative, "sha256": digest, "size_bytes": size})
    wrapper = _wrapper_values(source_root / "kmp_module/gradle/wrapper/gradle-wrapper.properties")
    closure = _closure_evidence(closure_path, source_commit, source_tree)
    try:
        closure_relative = closure_path.resolve(strict=True).relative_to(source_root.resolve(strict=True)).as_posix()
    except (OSError, ValueError) as error:
        raise ValueError("dependency closure must be beneath the exact source root") from error
    return {
        "schema": SCHEMA,
        "kind": KIND,
        "repository": "DobbyVPN/DobbyVPN",
        "source": {"commit": source_commit, "tree": source_tree},
        "closure": {
            "path": closure_relative,
            "sha256": closure["sha256"],
            "size_bytes": closure["size_bytes"],
            "artifact_root": closure["artifact_root"],
        },
        "dependency_provenance": "complete_owner_evidence",
        "resolution": {
            "mode": closure["mode"],
            "offline_verified": closure["offline_verified"],
            "evidence": "owner-captured-resolved-artifact-digests-and-complete-resolver-log",
            "cache_policy": "Only the byte-addressed artifacts enumerated in the owner closure are admissible.",
            "repositories": [
                {"id": "google", "url": "https://dl.google.com/dl/android/maven2/"},
                {"id": "mavenCentral", "url": "https://repo.maven.apache.org/maven2/"},
                {"id": "gradlePluginPortal", "url": "https://plugins.gradle.org/m2/"},
            ],
            "gradle_distribution": wrapper,
            "artifact_root": closure["artifact_root"],
            "resolved_artifacts": closure["resolved_artifacts"],
            "resolution_evidence": closure["resolution_evidence"],
            "verification_metadata": closure["verification_metadata"],
        },
        "go_modules": [
            {"module": "golang.org/x/mobile", "version": PINNED_X_MOBILE, "commands": ["gomobile", "gobind"]}
        ],
        "inputs": records + [
            {"path": f"dependency-closure/{closure['path']}", "sha256": closure["sha256"], "size_bytes": closure["size_bytes"]},
            {"path": f"dependency-closure/{closure['resolution_evidence']['path']}", "sha256": closure["resolution_evidence"]["sha256"], "size_bytes": closure["resolution_evidence"]["size_bytes"]},
            {"path": f"dependency-closure/{closure['verification_metadata']['path']}", "sha256": closure["verification_metadata"]["sha256"], "size_bytes": closure["verification_metadata"]["size_bytes"]},
            *[
                {
                    "path": f"dependency-closure/{closure['artifact_root']}/{artifact['path']}",
                    "sha256": artifact["sha256"],
                    "size_bytes": artifact["size_bytes"],
                }
                for artifact in closure["resolved_artifacts"]
            ],
        ],
    }


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
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-tree", required=True)
    parser.add_argument("--closure-evidence", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        _write_json(
            args.output,
            create_manifest(args.source_root, args.source_commit, args.source_tree, args.closure_evidence),
        )
    except (OSError, ValueError) as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
