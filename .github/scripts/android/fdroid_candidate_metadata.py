#!/usr/bin/env python3
"""Prepare and validate temporary F-Droid candidate metadata.

This helper checks metadata and build-recipe mechanics for a technical
prepublication run. It does not assess F-Droid catalog eligibility.
"""

from __future__ import annotations

import argparse
from copy import deepcopy
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from typing import Any
from urllib.parse import urlsplit

import yaml

from android_dependency_provenance import dependency_pins


COMMIT_SHA = re.compile(r"^[0-9a-f]{40}$")
APP_ID = "com.dobby.vpn"
REPRODUCIBLE_APK_TOOLS = "reproducible-apk-tools@v0.3.2"
GRADLE_OUTPUT = "app/build/outputs/apk/release/app-release-unsigned.apk"
UPDATE_CHECK_MODE = "HTTP"
AUTO_UPDATE_MODE = "Version v%v"


class CandidateMetadataError(ValueError):
    """Raised when F-Droid candidate metadata violates the technical contract."""


def _load_yaml(path: Path, label: str) -> dict[str, Any]:
    try:
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, yaml.YAMLError) as error:
        raise CandidateMetadataError(f"{label} is not readable YAML: {path}") from error
    if not isinstance(document, dict):
        raise CandidateMetadataError(f"{label} must contain a YAML mapping")
    if any(not isinstance(key, str) for key in document):
        raise CandidateMetadataError(f"{label} contains a non-string top-level key")
    # PyYAML applies YAML 1.1 booleans to bare `yes`/`no`, while fdroidserver's
    # metadata treats those Gradle flavor tokens as strings. Normalize only
    # this known field so an unchanged historical recipe stays unchanged.
    builds = document.get("Builds")
    if isinstance(builds, list):
        for build in builds:
            if isinstance(build, dict) and "gradle" in build:
                build["gradle"] = _normalize_gradle_value(build["gradle"])
    if isinstance(document.get("AllowedAPKSigningKeys"), str):
        document["AllowedAPKSigningKeys"] = [document["AllowedAPKSigningKeys"]]
    return document


def _normalize_gradle_value(value: Any) -> Any:
    if value is True:
        return "yes"
    if value is False:
        return "no"
    if isinstance(value, list):
        return [_normalize_gradle_value(item) for item in value]
    return value


def _write_yaml(path: Path, document: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    try:
        temporary.write_text(
            yaml.safe_dump(
                document,
                allow_unicode=True,
                sort_keys=False,
                width=1000,
            ),
            encoding="utf-8",
        )
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def _positive_version_code(value: Any, label: str) -> int:
    if isinstance(value, bool):
        raise CandidateMetadataError(f"{label} must be a positive Android version code")
    try:
        code = int(value)
    except (TypeError, ValueError) as error:
        raise CandidateMetadataError(f"{label} must be a positive Android version code") from error
    if code <= 0 or str(value).strip() != str(code):
        raise CandidateMetadataError(f"{label} must be a positive decimal Android version code")
    return code


def _version_inputs(version_name: str, version_code: Any) -> tuple[str, int]:
    if not isinstance(version_name, str) or not version_name.strip():
        raise CandidateMetadataError("version name must be a non-empty string")
    code = _positive_version_code(version_code, "version code")
    if code > 2_147_483_647:
        raise CandidateMetadataError("version code exceeds Android's supported integer range")
    return version_name.strip(), code


def _https_url(value: str, label: str) -> str:
    if not isinstance(value, str):
        raise CandidateMetadataError(f"{label} must be an HTTPS URL")
    parsed = urlsplit(value)
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.fragment
        or any(character.isspace() for character in value)
    ):
        raise CandidateMetadataError(f"{label} must be an HTTPS URL without credentials or fragment")
    return value


def _update_check_parts(document: dict[str, Any], label: str) -> list[str]:
    value = document.get("UpdateCheckData")
    if not isinstance(value, str):
        raise CandidateMetadataError(f"{label} UpdateCheckData must be a four-part string")
    parts = value.split("|")
    if len(parts) != 4:
        raise CandidateMetadataError(f"{label} UpdateCheckData must have exactly four pipe-separated fields")
    if not parts[1] or not parts[3]:
        raise CandidateMetadataError(f"{label} UpdateCheckData must include version-code and version-name regexes")
    for regex, regex_label in ((parts[1], "version-code"), (parts[3], "version-name")):
        try:
            compiled = re.compile(regex)
        except re.error as error:
            raise CandidateMetadataError(f"{label} UpdateCheckData has an invalid {regex_label} regex") from error
        if compiled.groups < 1:
            raise CandidateMetadataError(f"{label} {regex_label} regex must capture its value")
    return parts


def _validate_update_modes(document: dict[str, Any], label: str) -> list[str]:
    if document.get("UpdateCheckMode") != UPDATE_CHECK_MODE:
        raise CandidateMetadataError(f"{label} UpdateCheckMode must be {UPDATE_CHECK_MODE}")
    if document.get("AutoUpdateMode") != AUTO_UPDATE_MODE:
        raise CandidateMetadataError(f"{label} AutoUpdateMode must be {AUTO_UPDATE_MODE!r}")
    if document.get("UpdateCheckName") == "Ignore":
        raise CandidateMetadataError(f"{label} UpdateCheckName: Ignore disables package-name lookup")
    if document.get("Disabled"):
        raise CandidateMetadataError(f"{label} is disabled")
    return _update_check_parts(document, label)


def _historical_builds(document: dict[str, Any], label: str) -> list[dict[str, Any]]:
    builds = document.get("Builds")
    if not isinstance(builds, list) or not builds:
        raise CandidateMetadataError(f"{label} Builds must be a non-empty list")
    seen_codes: set[int] = set()
    for index, build in enumerate(builds):
        if not isinstance(build, dict):
            raise CandidateMetadataError(f"{label} Builds[{index}] must be a mapping")
        name = build.get("versionName")
        if not isinstance(name, str) or not name.strip():
            raise CandidateMetadataError(f"{label} Builds[{index}] has no versionName")
        code = _positive_version_code(build.get("versionCode"), f"{label} Builds[{index}] versionCode")
        if code in seen_codes:
            raise CandidateMetadataError(f"{label} has duplicate historical versionCode {code}")
        seen_codes.add(code)
        if not isinstance(build.get("commit"), str) or not build["commit"].strip():
            raise CandidateMetadataError(f"{label} Builds[{index}] has no source commit")
    return builds


def _validate_baseline(document: dict[str, Any], version_name: str, version_code: int) -> None:
    if document.get("RepoType") != "git":
        raise CandidateMetadataError("baseline RepoType must be git")
    if not isinstance(document.get("Repo"), str) or not document["Repo"].strip():
        raise CandidateMetadataError("baseline Repo must be a non-empty Git URL")
    license_value = document.get("License")
    if not isinstance(license_value, str) or not license_value.strip():
        raise CandidateMetadataError("baseline License must be a non-empty string")
    signing_keys = document.get("AllowedAPKSigningKeys", [])
    if not isinstance(signing_keys, list):
        raise CandidateMetadataError("AllowedAPKSigningKeys must be a string or list when present")
    if any(not isinstance(key, str) or not key for key in signing_keys):
        raise CandidateMetadataError("AllowedAPKSigningKeys must contain non-empty strings")
    if "CurrentVersionCode" in document:
        _positive_version_code(document["CurrentVersionCode"], "baseline CurrentVersionCode")
    if "CurrentVersion" in document and not isinstance(document["CurrentVersion"], str):
        raise CandidateMetadataError("baseline CurrentVersion must be a string when present")
    _validate_update_modes(document, "baseline")
    builds = _historical_builds(document, "baseline")
    highest_code = max(
        [int(build["versionCode"]) for build in builds]
        + ([int(document["CurrentVersionCode"])] if "CurrentVersionCode" in document else [])
    )
    if version_code <= highest_code:
        raise CandidateMetadataError(
            f"candidate versionCode {version_code} must be newer than historical versionCode {highest_code}"
        )
    if any(build["versionName"] == version_name for build in builds):
        raise CandidateMetadataError(f"candidate versionName {version_name!r} already exists in Builds")


def _file_repo_url(directory: Path) -> str:
    try:
        resolved = directory.expanduser().resolve(strict=True)
    except OSError as error:
        raise CandidateMetadataError(f"candidate Git mirror is unavailable: {directory}") from error
    if not resolved.is_dir():
        raise CandidateMetadataError(f"candidate Git mirror must be a directory: {resolved}")
    return resolved.as_uri()


def prepare_metadata(
    metadata_path: Path,
    baseline_path: Path,
    mirror_repo: Path,
    version_url: str,
    version_name: str,
    version_code: Any,
) -> dict[str, Any]:
    version_name, version_code = _version_inputs(version_name, version_code)
    version_url = _https_url(version_url, "version URL")
    metadata_path = metadata_path.resolve()
    baseline_path = baseline_path.resolve()
    if metadata_path == baseline_path:
        raise CandidateMetadataError("metadata and baseline paths must be different")

    live = _load_yaml(metadata_path, "metadata")
    _validate_baseline(live, version_name, version_code)
    parts = _validate_update_modes(live, "metadata")
    repo_url = _file_repo_url(mirror_repo)

    # The baseline is a byte-for-byte copy of the selected live fdroiddata YAML.
    baseline_path.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(metadata_path, baseline_path)

    candidate = deepcopy(live)
    candidate["Repo"] = repo_url
    parts[0] = version_url
    if parts[2] != ".":
        parts[2] = "."
    candidate["UpdateCheckData"] = "|".join(parts)
    _write_yaml(metadata_path, candidate)
    return {
        "phase": "prepare",
        "version_name": version_name,
        "version_code": version_code,
        "repo": repo_url,
        "version_url": version_url,
        "baseline": str(baseline_path),
    }


def _observed_local_repo(value: Any) -> Path:
    if not isinstance(value, str):
        raise CandidateMetadataError("candidate Repo must remain a local file URL")
    parsed = urlsplit(value)
    if parsed.scheme != "file" or parsed.netloc not in ("", "localhost") or parsed.query or parsed.fragment:
        raise CandidateMetadataError("candidate Repo must remain the isolated local Git mirror URL")
    path = Path(parsed.path)
    if not path.is_absolute() or not path.is_dir():
        raise CandidateMetadataError("candidate local Git mirror is unavailable during finalization")
    return path


def _assert_candidate_update_fields(
    baseline: dict[str, Any], observed: dict[str, Any]
) -> None:
    if observed.get("RepoType") != "git":
        raise CandidateMetadataError("candidate RepoType changed from git")
    _observed_local_repo(observed.get("Repo"))
    if observed.get("License") != baseline.get("License"):
        raise CandidateMetadataError("License changed during fdroid checkupdates")
    if observed.get("AllowedAPKSigningKeys") != baseline.get("AllowedAPKSigningKeys"):
        raise CandidateMetadataError("AllowedAPKSigningKeys changed during candidate preparation")
    if observed.get("UpdateCheckMode") != baseline.get("UpdateCheckMode"):
        raise CandidateMetadataError("UpdateCheckMode changed during candidate preparation")
    if observed.get("AutoUpdateMode") != baseline.get("AutoUpdateMode"):
        raise CandidateMetadataError("AutoUpdateMode changed during candidate preparation")
    if observed.get("UpdateCheckName") != baseline.get("UpdateCheckName"):
        raise CandidateMetadataError("UpdateCheckName changed during candidate preparation")
    baseline_parts = _validate_update_modes(baseline, "baseline")
    observed_parts = _validate_update_modes(observed, "candidate")
    if observed_parts[1] != baseline_parts[1] or observed_parts[3] != baseline_parts[3]:
        raise CandidateMetadataError("candidate UpdateCheckData changed a historical version regex")
    if observed_parts[2] != ".":
        raise CandidateMetadataError("candidate UpdateCheckData must read both versions from its HTTPS document")
    _https_url(observed_parts[0], "candidate UpdateCheckData URL")


def _assert_only_update_fields_changed(
    baseline: dict[str, Any], observed: dict[str, Any]
) -> None:
    allowed = {
        "AutoName",
        "Builds",
        "CurrentVersion",
        "CurrentVersionCode",
        "Repo",
        "UpdateCheckData",
    }
    keys = set(baseline) | set(observed)
    changed = {
        key for key in keys
        if baseline.get(key, _MISSING) != observed.get(key, _MISSING)
    }
    unexpected = changed - allowed
    if unexpected:
        raise CandidateMetadataError(
            "fdroid checkupdates changed unexpected top-level metadata keys: "
            + ", ".join(sorted(unexpected))
        )
    if "AutoName" in observed:
        name = observed["AutoName"]
        if not isinstance(name, str) or not name.strip():
            raise CandidateMetadataError("fdroid checkupdates produced an invalid AutoName")
    elif "AutoName" in baseline:
        raise CandidateMetadataError("fdroid checkupdates removed the existing AutoName")


_MISSING = object()


def _source_commit(source_root: Path, expected: str) -> None:
    if not COMMIT_SHA.fullmatch(expected):
        raise CandidateMetadataError("source SHA must be a full lowercase Git commit SHA")
    try:
        result = subprocess.run(
            ["git", "-C", str(source_root), "rev-parse", "--verify", "HEAD^{commit}"],
            check=False,
            capture_output=True,
        )
    except OSError as error:
        raise CandidateMetadataError(f"source checkout is not a readable Git repository: {source_root}") from error
    sys.stdout.buffer.write(result.stdout)
    sys.stdout.buffer.flush()
    sys.stderr.buffer.write(result.stderr)
    sys.stderr.buffer.flush()
    result.check_returncode()
    observed = result.stdout.decode("utf-8", errors="strict").strip()
    if observed != expected:
        raise CandidateMetadataError(
            f"source checkout SHA {observed!r} does not match expected {expected}"
        )


def _android_package_name(source_root: Path) -> str:
    properties = source_root / "ui/android/gradle.properties"
    try:
        lines = properties.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeError) as error:
        raise CandidateMetadataError("Android Gradle properties are unavailable") from error
    values = {
        key.strip(): value.strip()
        for line in lines
        if line.strip() and not line.lstrip().startswith("#") and "=" in line
        for key, value in [line.split("=", 1)]
    }
    package_name = values.get("packageName")
    if not package_name or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+", package_name):
        raise CandidateMetadataError("ui/android/gradle.properties must declare a valid packageName")
    return package_name


def _build_recipe(source_root: Path, source_sha: str, version_name: str, version_code: int) -> dict[str, Any]:
    try:
        pins = dependency_pins(source_root)
    except (OSError, ValueError) as error:
        raise CandidateMetadataError(f"Android dependency pins are unavailable: {error}") from error
    java_major = int(pins["java_major"])
    if java_major != 17:
        raise CandidateMetadataError(f"Android source requires JDK 17, dependency pins report JDK {java_major}")

    go_version = str(pins["go_version"])
    go_source_commit = str(pins["go_source_commit"])
    compile_sdk = int(pins["android_compile_sdk"])
    build_tools = str(pins["android_build_tools"])
    ndk = str(pins["android_ndk"])
    package_name = _android_package_name(source_root)

    build_script = f'''set -euo pipefail
product_root="$(cd ../.. && pwd)"
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
export PATH="$JAVA_HOME/bin:$PATH"
export GOROOT="$$go$$"
export GOROOT_FINAL="$GOROOT"
export GOPATH=/home/vagrant/go
export GOTOOLCHAIN=local
export GOFLAGS="-trimpath -buildvcs=false"
export SOURCE_DATE_EPOCH=0
export ANDROID_HOME="$$SDK$$"
export ANDROID_SDK_ROOT="$$SDK$$"
export ANDROID_NDK_HOME="$$NDK$$"
sdkmanager --sdk_root="$ANDROID_SDK_ROOT" "platforms;android-{compile_sdk}" "build-tools;{build_tools}"
java_output="$(java -version 2>&1)"
printf '%s\\n' "$java_output"
case "$java_output" in *'version "17.'*) ;; *) exit 1 ;; esac
go_root="$$go$$"
test "$(git -C "$go_root" rev-parse --verify HEAD^{{commit}})" = "{go_source_commit}"
bootstrap_go="$(command -v go)"
test -n "$bootstrap_go"
export GOROOT_BOOTSTRAP="$(dirname "$(dirname "$(readlink -f "$bootstrap_go")")")"
cd "$GOROOT/src"
./make.bash
"$GOROOT/bin/go" version
cd "$product_root/core"
"$GOROOT/bin/go" mod download
# F-Droid adds legacy ndk.dir; the Android project owns the pinned android.ndkPath.
sed -i '/^[[:space:]]*ndk[.]dir[[:space:]]*=/d' "$product_root/ui/android/local.properties"
'''

    return {
        "subdir": "ui/android",
        "gradle": ["yes"],
        "srclibs": [f"go@go{go_version}", REPRODUCIBLE_APK_TOOLS],
        "sudo": [
            "apt-get install -y -t trixie-backports golang-go",
        ],
        "target": f"android-{compile_sdk}",
        "ndk": ndk,
        "build": [build_script],
        "gradleprops": [
            f"android.injected.version.name={version_name}",
            f"android.injected.version.code={version_code}",
            f"packageName={package_name}",
            f"projectRepositoryCommit={source_sha}",
            f"dobbyGoBinary={source_root.resolve().parent / 'srclib/go/bin/go'}",
        ],
        "output": GRADLE_OUTPUT,
    }


def finalize_metadata(
    metadata_path: Path,
    baseline_path: Path,
    source_root: Path,
    source_sha: str,
    version_name: str,
    version_code: Any,
) -> dict[str, Any]:
    version_name, version_code = _version_inputs(version_name, version_code)
    metadata_path = metadata_path.resolve()
    baseline_path = baseline_path.resolve()
    baseline = _load_yaml(baseline_path, "baseline")
    _validate_baseline(baseline, version_name, version_code)
    observed = _load_yaml(metadata_path, "candidate metadata")
    _assert_candidate_update_fields(baseline, observed)
    _assert_only_update_fields_changed(baseline, observed)
    _source_commit(source_root, source_sha)

    historical = _historical_builds(baseline, "baseline")
    builds = observed.get("Builds")
    if not isinstance(builds, list) or len(builds) != len(historical) + 1:
        raise CandidateMetadataError("fdroid checkupdates --auto must append exactly one Build entry")
    if builds[: len(historical)] != historical:
        raise CandidateMetadataError("historical Builds changed during fdroid checkupdates --auto")
    new_build = builds[-1]
    if not isinstance(new_build, dict):
        raise CandidateMetadataError("the appended Build entry must be a mapping")
    if new_build.get("versionName") != version_name:
        raise CandidateMetadataError("the appended Build versionName does not match the candidate")
    if _positive_version_code(new_build.get("versionCode"), "appended Build versionCode") != version_code:
        raise CandidateMetadataError("the appended Build versionCode does not match the candidate")
    if new_build.get("commit") != source_sha:
        raise CandidateMetadataError("the appended Build commit does not match the selected source SHA")
    if observed.get("CurrentVersion") != version_name:
        raise CandidateMetadataError("CurrentVersion does not match the candidate versionName")
    if _positive_version_code(observed.get("CurrentVersionCode"), "CurrentVersionCode") != version_code:
        raise CandidateMetadataError("CurrentVersionCode does not match the candidate versionCode")

    recipe = _build_recipe(source_root, source_sha, version_name, version_code)
    finalized = deepcopy(observed)
    patched_build = deepcopy(new_build)
    patched_build.pop("preassemble", None)
    patched_build.pop("rm", None)
    patched_build.update(recipe)
    finalized["Builds"] = [*builds[:-1], patched_build]
    finalized.pop("Binaries", None)
    finalized["License"] = "BUSL-1.1"
    _write_yaml(metadata_path, finalized)
    return {
        "phase": "finalize",
        "version_name": version_name,
        "version_code": version_code,
        "source_sha": source_sha,
        "go_source_commit": str(dependency_pins(source_root)["go_source_commit"]),
        "build_output": GRADLE_OUTPUT,
        "metadata": str(metadata_path),
    }


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="phase", required=True)

    prepare = subparsers.add_parser("prepare", help="copy the live baseline and isolate candidate update inputs")
    prepare.add_argument("--metadata", type=Path, required=True)
    prepare.add_argument("--baseline", type=Path, required=True)
    prepare.add_argument("--mirror-repo", type=Path, required=True)
    prepare.add_argument("--version-url", required=True)
    prepare.add_argument("--version-name", required=True)
    prepare.add_argument("--version-code", required=True)

    finalize = subparsers.add_parser("finalize", help="verify real checkupdates output and pin the appended recipe")
    finalize.add_argument("--metadata", type=Path, required=True)
    finalize.add_argument("--baseline", type=Path, required=True)
    finalize.add_argument("--source-root", type=Path, required=True)
    finalize.add_argument("--source-sha", required=True)
    finalize.add_argument("--version-name", required=True)
    finalize.add_argument("--version-code", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.phase == "prepare":
            result = prepare_metadata(
                args.metadata,
                args.baseline,
                args.mirror_repo,
                args.version_url,
                args.version_name,
                args.version_code,
            )
        else:
            result = finalize_metadata(
                args.metadata,
                args.baseline,
                args.source_root,
                args.source_sha,
                args.version_name,
                args.version_code,
            )
    except CandidateMetadataError as error:
        print(f"fdroid candidate metadata: {error}", file=sys.stderr)
        return 2
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
