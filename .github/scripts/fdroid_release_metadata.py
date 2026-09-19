#!/usr/bin/env python3
"""Prepare and validate a temporary F-Droid release metadata candidate."""

from __future__ import annotations

import argparse
from pathlib import Path
import re
import sys
from typing import Any

import yaml


SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
VERSION_NAME = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$"
)
ALLOWED_FINAL_FIELDS = {
    "Builds",
    "Binaries",
    "CurrentVersion",
    "CurrentVersionCode",
    "UpdateCheckData",
}
GO_FYNE_SOURCES = [
    "go@go1.26.8",
    "reproducible-apk-tools@v0.3.2",
]
GO_FYNE_BUILD = [
    "pushd $$go$$/src",
    "./make.bash",
    "popd",
    "export GOROOT=$$go$$",
    'export GOPATH="$HOME/go"',
    'export GO111MODULE=on',
    'export GOFLAGS="-trimpath -buildvcs=false"',
    'export GOTOOLCHAIN="local"',
    'export SOURCE_DATE_EPOCH=0',
    'export PATH="$GOROOT/bin:$GOPATH/bin:$PATH"',
    'test -x "$GOROOT/bin/go"',
    'test "$("$GOROOT/bin/go" env GOVERSION)" = "go1.26.8"',
    'export ORG_GRADLE_PROJECT_dobbyGoBinary="$GOROOT/bin/go"',
    'go env GOROOT GOVERSION GOFLAGS GOTOOLCHAIN',
    'cd ..',
    'export REPO_ROOT=$(pwd)',
    'pushd go_module',
    'go mod download',
    'popd',
    'sdkmanager "platforms;android-35" "platforms;android-36" "build-tools;36.0.0" "ndk;27.3.13750724"',
    'export ANDROID_SDK_ROOT="$$SDK$$"',
    'export ANDROID_HOME="$$SDK$$"',
    'export ANDROID_NDK_HOME="$$SDK$$/ndk/27.3.13750724"',
    'export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64',
    'export PATH="$JAVA_HOME/bin:$GOROOT/bin:$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin:$GOPATH/bin:$PATH"',
    'cd android_module',
    'sed -i -e "s/^versionCode=.*/versionCode=$$VERCODE$$/" -e "s/^versionName=.*/versionName=$$VERSION$$/" gradle.properties',
    'export COMMIT=$(git rev-parse HEAD)',
    'printf "\\nprojectRepositoryCommit=$COMMIT\\nprojectRepositoryCommitLink=https://github.com/DobbyVPN/DobbyVPN/tree/$COMMIT\\n" >> gradle.properties',
]


class MetadataError(ValueError):
    """The metadata cannot describe the requested release candidate."""


def _read(path: Path) -> dict[str, Any]:
    try:
        value = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, yaml.YAMLError) as error:
        raise MetadataError(f"cannot read metadata {path}: {error}") from error
    if not isinstance(value, dict):
        raise MetadataError("metadata must be a YAML mapping")
    _normalize_gradle_flags(value)
    return value


def _normalize_gradle_flags(document: dict[str, Any]) -> None:
    """Keep F-Droid's default Gradle flavor as the string ``yes``.

    PyYAML follows YAML 1.1 and reads the upstream recipe's ``gradle: yes``
    as a boolean.  F-Droid treats the literal flavor ``yes`` specially (it
    means the default release task); writing the boolean back would make the
    current server generate ``assembleTrueRelease`` instead.
    """
    builds = document.get("Builds")
    if not isinstance(builds, list):
        return
    for build in builds:
        if not isinstance(build, dict) or "gradle" not in build:
            continue
        value = build["gradle"]
        if isinstance(value, bool):
            build["gradle"] = ["yes"] if value else []
        elif isinstance(value, list):
            build["gradle"] = ["yes" if item is True else item for item in value]


def _write(path: Path, value: dict[str, Any]) -> None:
    try:
        path.write_text(
            yaml.safe_dump(value, sort_keys=False, allow_unicode=True),
            encoding="utf-8",
        )
    except (OSError, yaml.YAMLError) as error:
        raise MetadataError(f"cannot write metadata {path}: {error}") from error


def _version_code(build: dict[str, Any]) -> int:
    value = build.get("versionCode")
    if isinstance(value, bool):
        raise MetadataError("versionCode must be an integer")
    try:
        result = int(value)
    except (TypeError, ValueError) as error:
        raise MetadataError("every build must have an integer versionCode") from error
    if result < 1:
        raise MetadataError("every build versionCode must be positive")
    return result


def _validate_request(version_name: str, version_code: int, source_sha: str | None = None) -> None:
    if not VERSION_NAME.fullmatch(version_name):
        raise MetadataError("version name must use X.Y.Z format")
    if version_code < 1:
        raise MetadataError("version code must be positive")
    if source_sha is not None and not SOURCE_SHA.fullmatch(source_sha):
        raise MetadataError("source SHA must be a lowercase 40-character Git SHA")


def _validate_document(document: dict[str, Any]) -> list[dict[str, Any]]:
    builds = document.get("Builds")
    if not isinstance(builds, list) or not builds:
        raise MetadataError("metadata must contain a non-empty Builds list")
    if not isinstance(document.get("Binaries"), str) or not document["Binaries"]:
        raise MetadataError("metadata must contain a non-empty Binaries URL")
    keys = document.get("AllowedAPKSigningKeys")
    if isinstance(keys, str):
        if not keys.strip():
            raise MetadataError("AllowedAPKSigningKeys must not be empty")
    elif isinstance(keys, list) and keys and all(isinstance(key, str) and key for key in keys):
        pass
    else:
        raise MetadataError("AllowedAPKSigningKeys must contain a signing key")
    if "UpdateCheckData" not in document:
        raise MetadataError("metadata must contain UpdateCheckData")
    for build in builds:
        if not isinstance(build, dict):
            raise MetadataError("every Builds entry must be a mapping")
        _version_code(build)
        if not isinstance(build.get("versionName"), str) or not build["versionName"]:
            raise MetadataError("every build must have a versionName")
        if not isinstance(build.get("commit"), str) or not build["commit"]:
            raise MetadataError("every build must have a commit")
    return builds


def _target_builds(
    builds: list[dict[str, Any]], version_name: str, version_code: int
) -> list[dict[str, Any]]:
    return [
        build
        for build in builds
        if _version_code(build) == version_code
        and build.get("versionName") == version_name
    ]


def _canonical(value: Any, field: str | None = None) -> Any:
    """Compare YAML before and after fdroidserver's metadata normalization."""
    if field == "gradle" and isinstance(value, list):
        return [_canonical(item, field) for item in value]
    if field == "gradle" and isinstance(value, bool):
        return "yes" if value else ""
    if field == "gradle" and isinstance(value, str):
        return value.lower()
    if isinstance(value, dict):
        return {key: _canonical(item, key) for key, item in value.items()}
    if isinstance(value, list):
        return [_canonical(item) for item in value]
    return value


def _same(left: Any, right: Any) -> bool:
    return _canonical(left) == _canonical(right)


def prepare(
    metadata_path: Path,
    baseline_path: Path,
    version_name: str,
    version_code: int,
) -> str:
    _validate_request(version_name, version_code)
    document = _read(metadata_path)
    builds = _validate_document(document)
    if metadata_path.resolve() == baseline_path.resolve():
        raise MetadataError("baseline metadata must be a separate file")
    _write(baseline_path, document)

    matching_code = [build for build in builds if _version_code(build) == version_code]
    if matching_code:
        if len(matching_code) != 1 or matching_code[0].get("versionName") != version_name:
            raise MetadataError("requested versionCode already has a different recipe")
        print("existing")
        return "existing"

    same_name = [build for build in builds if build.get("versionName") == version_name]
    if same_name:
        raise MetadataError("requested versionName already has a different versionCode")
    if version_code <= max(_version_code(build) for build in builds):
        raise MetadataError("candidate versionCode must be newer than every recipe")

    _write(metadata_path, document)
    print("candidate")
    return "candidate"


def autoupdate(metadata_path: Path, version_name: str, version_code: int) -> None:
    """Run the checked-out fdroidserver update logic with fixed release data."""
    _validate_request(version_name, version_code)
    try:
        from fdroidserver import checkupdates, metadata
    except ImportError as error:
        raise MetadataError(
            "fdroidserver must be available through PYTHONPATH for autoupdate"
        ) from error

    app = metadata.parse_metadata(metadata_path)
    checkupdates.check_http = lambda _app: (version_name, version_code)
    checkupdates.fetch_autoname = lambda _app, _tag: None

    class UnresolvedVcs:
        def getref(self, _reference: str) -> None:
            return None

    checkupdates.common.getvcs = lambda *_args, **_kwargs: UnresolvedVcs()
    checkupdates.checkupdates_app(app, auto=True, make_commit=False)


def finalize(
    metadata_path: Path,
    baseline_path: Path,
    mode: str,
    version_name: str,
    version_code: int,
    source_sha: str,
    binary_url: str,
) -> None:
    _validate_request(version_name, version_code, source_sha)
    if mode not in {"candidate", "existing"}:
        raise MetadataError("mode must be candidate or existing")
    if not binary_url.startswith(("http://127.0.0.1:", "https://127.0.0.1:")) \
        or "%v" not in binary_url:
        raise MetadataError("Binaries test URL must use loopback HTTP(S) and %v")

    baseline = _read(baseline_path)
    document = _read(metadata_path)
    baseline_builds = _validate_document(baseline)
    builds = _validate_document(document)

    for key in set(baseline) | set(document):
        if key not in ALLOWED_FINAL_FIELDS and not _same(
            baseline.get(key), document.get(key)
        ):
            raise MetadataError(f"unexpected metadata change in {key}")

    if mode == "candidate":
        if len(builds) != len(baseline_builds) + 1:
            raise MetadataError("checkupdates did not append exactly one build")
        if not _same(builds[:-1], baseline_builds):
            raise MetadataError("checkupdates changed an existing build recipe")
    elif not _same(builds, baseline_builds):
        raise MetadataError("existing recipe mode changed the build list")

    targets = _target_builds(builds, version_name, version_code)
    if len(targets) != 1:
        raise MetadataError("metadata must contain exactly one requested build")
    if mode == "candidate" and _version_code(builds[-1]) != version_code:
        raise MetadataError("checkupdates appended the wrong versionCode")
    if mode == "candidate" and builds[-1].get("versionName") != version_name:
        raise MetadataError("checkupdates appended the wrong versionName")

    target = targets[0]
    if not isinstance(target.get("commit"), str) or not target["commit"]:
        raise MetadataError("requested build has no source commit")
    target["commit"] = source_sha
    # The release shell is a plain Android project now. Point the candidate at
    # the one Gradle root that owns the Go/Fyne APK and replace the inherited
    # Legacy mobile recipe with the pinned Go/Fyne build inputs.
    target["subdir"] = "android_module"
    target["gradle"] = ["yes"]
    target["srclibs"] = list(GO_FYNE_SOURCES)
    target["rm"] = ["swift_module"]
    target["build"] = list(GO_FYNE_BUILD)
    target.pop("preassemble", None)
    document["Binaries"] = binary_url
    document["UpdateCheckData"] = baseline["UpdateCheckData"]
    document["CurrentVersion"] = version_name
    document["CurrentVersionCode"] = version_code
    _write(metadata_path, document)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)

    prepare_parser = commands.add_parser("prepare")
    prepare_parser.add_argument("--metadata", type=Path, required=True)
    prepare_parser.add_argument("--baseline", type=Path, required=True)
    prepare_parser.add_argument("--version-name", required=True)
    prepare_parser.add_argument("--version-code", type=int, required=True)

    autoupdate_parser = commands.add_parser("autoupdate")
    autoupdate_parser.add_argument("--metadata", type=Path, required=True)
    autoupdate_parser.add_argument("--version-name", required=True)
    autoupdate_parser.add_argument("--version-code", type=int, required=True)

    finalize_parser = commands.add_parser("finalize")
    finalize_parser.add_argument("--metadata", type=Path, required=True)
    finalize_parser.add_argument("--baseline", type=Path, required=True)
    finalize_parser.add_argument("--mode", choices=("candidate", "existing"), required=True)
    finalize_parser.add_argument("--version-name", required=True)
    finalize_parser.add_argument("--version-code", type=int, required=True)
    finalize_parser.add_argument("--source-sha", required=True)
    finalize_parser.add_argument("--binary-url", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "prepare":
            prepare(
                args.metadata,
                args.baseline,
                args.version_name,
                args.version_code,
            )
        elif args.command == "autoupdate":
            autoupdate(args.metadata, args.version_name, args.version_code)
        else:
            finalize(
                args.metadata,
                args.baseline,
                args.mode,
                args.version_name,
                args.version_code,
                args.source_sha,
                args.binary_url,
            )
    except MetadataError as error:
        print(f"fdroid metadata error: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
