#!/usr/bin/env python3
"""Validate Android dependency pins and write build provenance."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
from typing import Any


SHA40 = re.compile(r"^[0-9a-f]{40}$")
SCHEMA = 1
KIND = "dobbyvpn.android.dependency-spec"
MOBILE_MODULE = "golang.org/x/mobile"
MOBILE_VERSION = "v0.0.0-20260520154334-0e4426e1883d"
GO_VERSION = "1.26.8"
GO_SOURCE_COMMIT = "c293dd49cbe25e1fe8d97d94a5cb618e7b6d831e"
FYNE_MODULE = "fyne.io/fyne/v2"
FYNE_VERSION = "v2.8.1"
FYNE_REPLACEMENT_MODULE = "github.com/DobbyVPN/fyne/v2"
FYNE_REPLACEMENT_VERSION = "v2.0.0-20260921083927-44c5d29914a2"
FYNE_REVISION = "44c5d29914a2760a0358215dddaf8f295c9ae7dc"
GLFW_MODULE = "github.com/go-gl/glfw/v3.4/glfw"
GLFW_VERSION = "v0.1.0-pre.1.0.20260707082822-2a407d02d01a"
GLFW_REPLACEMENT_MODULE = "github.com/DobbyVPN/glfw/v3.4/glfw"
GLFW_REPLACEMENT_VERSION = "v0.0.0-20260921083927-e9a15d43604f"
GLFW_REVISION = "e9a15d43604f85750bb1129227694b2a01512ec9"
GO_UI_MODULES = (
    {
        "module": FYNE_MODULE,
        "version": FYNE_VERSION,
        "replacement_module": FYNE_REPLACEMENT_MODULE,
        "replacement_version": FYNE_REPLACEMENT_VERSION,
        "revision": FYNE_REVISION,
        "commands": ["copyFyneJava", "go build"],
    },
    {
        "module": GLFW_MODULE,
        "version": GLFW_VERSION,
        "replacement_module": GLFW_REPLACEMENT_MODULE,
        "replacement_version": GLFW_REPLACEMENT_VERSION,
        "revision": GLFW_REVISION,
        "commands": ["go build"],
    },
)
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
    "android_module/settings.gradle.kts",
    "android_module/build.gradle.kts",
    "android_module/app/build.gradle.kts",
    "android_module/gradle/wrapper/gradle-wrapper.properties",
    "android_module/gradle/wrapper/gradle-wrapper.jar",
    "android_module/gradle.properties",
    "go_module/go.mod",
    "go_module/go.sum",
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
    expected_keys = {"schema", "kind", "repositories", "gradle", "java", "android", "go", "go_mobile"}
    if set(document) != expected_keys:
        raise ValueError("dependency specification has unexpected or missing fields")
    if document["schema"] != SCHEMA or document["kind"] != KIND:
        raise ValueError("dependency specification has an unsupported schema")
    if document["repositories"] != REPOSITORIES:
        raise ValueError("dependency repositories are not the approved repository set")
    gradle = document["gradle"]
    if not isinstance(gradle, dict) or set(gradle) != {"distribution_url", "distribution_sha256"}:
        raise ValueError("dependency Gradle pin is incomplete")
    if gradle != {"distribution_url": GRADLE_URL, "distribution_sha256": GRADLE_SHA256}:
        raise ValueError("dependency Gradle pin is not the approved immutable distribution")
    java = document["java"]
    android = document["android"]
    go = document["go"]
    mobile = document["go_mobile"]
    if not isinstance(java, dict) or java != {"major": JAVA_MAJOR}:
        raise ValueError("Java pin is not the approved major version")
    if not isinstance(android, dict) or android != {"build_tools": ANDROID_BUILD_TOOLS, "ndk": ANDROID_NDK}:
        raise ValueError("Android toolchain pin is not the approved exact set")
    if not isinstance(go, dict) or go != {"version": GO_VERSION, "source_commit": GO_SOURCE_COMMIT}:
        raise ValueError("Go pin is not the approved version")
    if not isinstance(mobile, dict) or mobile != {"module": MOBILE_MODULE, "version": MOBILE_VERSION}:
        raise ValueError("x/mobile pin is not the approved immutable revision")
    return document


def _verify_external_gradle_distribution(archive: Path, root: Path) -> dict[str, object]:
    if not archive.is_file():
        raise ValueError("external Gradle archive is unavailable")
    archive_sha256 = _sha256(archive)
    if archive_sha256 != GRADLE_SHA256:
        raise ValueError("external Gradle archive SHA-256 does not match the trusted spec")
    gradle_entry = root / "bin/gradle"
    if not gradle_entry.is_file() or not (gradle_entry.stat().st_mode & 0o100):
        raise ValueError("external Gradle root does not contain an executable bin/gradle")
    return {
        "archive_sha256": archive_sha256,
        "sha256": GRADLE_SHA256,
        "source": "external_verified_archive",
        "url": GRADLE_URL,
        "version": GRADLE_VERSION,
    }


def _wrapper_values(path: Path) -> dict[str, object]:
    values: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key] = value.replace("\\:", ":")
    if values.get("distributionUrl") != GRADLE_URL:
        raise ValueError("Gradle wrapper distribution is not the pinned 8.13 binary")
    if values.get("distributionSha256Sum", "").lower() != GRADLE_SHA256:
        raise ValueError("Gradle wrapper must contain the tracked distribution SHA-256")
    return {"source": "wrapper_checksum", "url": GRADLE_URL, "sha256": GRADLE_SHA256, "version": GRADLE_VERSION}


def _spec_location(
    source_root: Path,
    spec_path: Path,
) -> tuple[Path, str]:
    try:
        relative = spec_path.absolute().relative_to(source_root.absolute()).as_posix()
    except ValueError as error:
        raise ValueError("dependency specification must be beneath its source root") from error
    path, _digest, _size = _regular_file_beneath(source_root, relative, "dependency specification")
    return path, relative


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
    java_version: str = str(JAVA_MAJOR),
    gradle_archive: Path | None = None,
    gradle_root: Path | None = None,
) -> dict[str, object]:
    if not SHA40.fullmatch(source_commit) or not SHA40.fullmatch(source_tree):
        raise ValueError("source commit/tree must be full lowercase Git identities")
    _source_go_version(source_root)
    if not isinstance(java_version, str) or not (
        java_version == str(JAVA_MAJOR) or java_version.startswith(f"{JAVA_MAJOR}.")
    ):
        raise ValueError(f"observed Java version must have major {JAVA_MAJOR}, got {java_version!r}")
    if (gradle_archive is None) != (gradle_root is None):
        raise ValueError("external Gradle archive and root proof must be supplied together")
    spec_file, spec_relative = _spec_location(source_root, spec_path)
    spec = _read_spec(spec_file)
    inputs: list[dict[str, object]] = []
    for relative in DECLARED_INPUTS:
        _path, digest, size = _regular_file_beneath(source_root, relative, "dependency input")
        inputs.append({"path": relative, "sha256": digest, "size_bytes": size})
    gradle_distribution = (
        _verify_external_gradle_distribution(gradle_archive, gradle_root)
        if gradle_archive is not None and gradle_root is not None
        else _wrapper_values(source_root / "android_module/gradle/wrapper/gradle-wrapper.properties")
    )
    spec_sha256 = _sha256(spec_file)
    spec_input: dict[str, object] = {
        "path": spec_relative,
        "sha256": spec_sha256,
        "size_bytes": spec_file.stat().st_size,
    }
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
            "evidence": "tracked source-level dependency declarations and toolchain pins; resolved bytes remain runner-local",
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
        "go_modules": [
            {"module": MOBILE_MODULE, "version": MOBILE_VERSION, "commands": ["go build -buildmode=c-shared"]},
            *[dict(module) for module in GO_UI_MODULES],
        ],
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
    java_version: str = str(JAVA_MAJOR),
    gradle_archive: Path | None = None,
    gradle_root: Path | None = None,
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
    parser.add_argument("--gradle-archive", type=Path)
    parser.add_argument("--gradle-root", type=Path)
    parser.add_argument("--verify-gradle-distribution", action="store_true")
    parser.add_argument("--print-mobile-version", action="store_true")
    parser.add_argument("--print-go-ui-replacements", action="store_true")
    parser.add_argument("--print-go-version", action="store_true")
    parser.add_argument("--print-go-source-commit", action="store_true")
    parser.add_argument("--print-gradle-url", action="store_true")
    parser.add_argument("--print-gradle-sha256", action="store_true")
    args = parser.parse_args(argv)
    try:
        if args.print_mobile_version:
            if args.spec:
                _read_spec(args.spec)
            print(f"{MOBILE_MODULE}@{MOBILE_VERSION}")
            return 0
        if args.print_go_ui_replacements:
            if args.spec:
                _read_spec(args.spec)
            for module in GO_UI_MODULES:
                print("\t".join(
                    str(module[key])
                    for key in (
                        "module",
                        "version",
                        "replacement_module",
                        "replacement_version",
                        "revision",
                    )
                ))
            return 0
        if args.print_go_source_commit:
            if args.spec:
                _read_spec(args.spec)
            print(GO_SOURCE_COMMIT)
            return 0
        if args.print_go_version:
            if args.spec:
                _read_spec(args.spec)
            print(GO_VERSION)
            return 0
        if args.print_gradle_url:
            if args.spec:
                _read_spec(args.spec)
            print(GRADLE_URL)
            return 0
        if args.print_gradle_sha256:
            if args.spec:
                _read_spec(args.spec)
            print(GRADLE_SHA256)
            return 0
        if args.verify_gradle_distribution:
            if args.gradle_archive is None or args.gradle_root is None:
                parser.error("--gradle-archive and --gradle-root are required for distribution verification")
            if args.spec:
                _read_spec(args.spec)
            print(json.dumps(_verify_external_gradle_distribution(args.gradle_archive, args.gradle_root), sort_keys=True))
            return 0
        if not args.source_root or not args.source_commit or not args.source_tree or not args.spec:
            parser.error("--source-root, --source-commit, --source-tree, and --spec are required")
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
