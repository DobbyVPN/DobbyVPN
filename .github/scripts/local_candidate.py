#!/usr/bin/env python3
"""Prepare a local DobbyVPN candidate and describe its runnable interfaces.

This is an intentionally small adapter around the product's existing build
helpers.  It owns no VPN operations or result semantics; callers use the
paths and process identities in the descriptor to start the candidate.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any


SCHEMA = 1
KIND = "dobbyvpn.local-candidate"
PLATFORMS = ("linux", "windows", "android", "macos")
DESKTOP_PLATFORMS = ("linux", "windows", "macos")
DEFAULT_ARCHITECTURES = {
    "linux": "amd64",
    "windows": "amd64",
    "android": "arm64-v8a",
    "macos": "arm64",
}
SERVICE_NAMES = {
    "linux": "ubuntu_grpcvpnserver",
    "windows": "windows_grpcvpnserver.exe",
    "macos": "macos_grpcvpnserver",
}
CLI_NAMES = {
    "linux": "dobby-cli",
    "windows": "dobby-cli.exe",
    "macos": "dobby-cli",
}
SERVICE_IDENTITIES = {
    "linux": "ubuntu_grpcvpnserver",
    "windows": "windows_grpcvpnserver.exe",
    "macos": "macos_grpcvpnserver",
}
APP_IDENTITY = "MainKt"
ANDROID_APP_IDENTITY = "com.dobby.vpn"
IDENTITY = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
SOURCE_SHA = re.compile(r"[0-9a-f]{40}\Z")


class CandidateError(ValueError):
    """Raised when a candidate request or its resulting descriptor is invalid."""


def _absolute(path: Path, label: str) -> Path:
    if not path.is_absolute():
        raise CandidateError(f"{label} must be an absolute path")
    if any(part in (".", "..") for part in path.parts):
        raise CandidateError(f"{label} must not contain dot components")
    return path


def _existing_directory(path: Path, label: str) -> Path:
    path = _absolute(path, label)
    if path.is_symlink() or not path.is_dir():
        raise CandidateError(f"{label} must be a non-symlink directory")
    return path.resolve(strict=True)


def _confined(path: Path, root: Path, label: str, *, must_exist: bool = False) -> Path:
    """Return a canonical path below root without traversing a symlink."""
    path = _absolute(path, label)
    root = root.resolve(strict=True)
    try:
        relative = path.relative_to(root)
    except ValueError as error:
        raise CandidateError(f"{label} must be below request root") from error
    if not relative.parts:
        raise CandidateError(f"{label} must not be the request root")
    current = root
    for part in relative.parts:
        current /= part
        if current.is_symlink():
            raise CandidateError(f"{label} must not traverse a symlink")
    if must_exist and not path.exists():
        raise CandidateError(f"{label} does not exist")
    if path.exists() or path.is_symlink():
        return path.resolve(strict=True)
    return path


def _new_directory(path: Path, root: Path, label: str) -> Path:
    path = _confined(path, root, label)
    if path.exists() or path.is_symlink():
        raise CandidateError(f"{label} already exists")
    try:
        path.mkdir(mode=0o700, parents=False)
    except OSError as error:
        raise CandidateError(f"could not create {label}: {error}") from error
    return path


def _new_file(path: Path, root: Path, label: str) -> Path:
    path = _confined(path, root, label)
    if path.exists() or path.is_symlink():
        raise CandidateError(f"{label} already exists")
    try:
        descriptor = os.open(
            path,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0),
            0o600,
        )
    except OSError as error:
        raise CandidateError(f"could not create {label}: {error}") from error
    os.close(descriptor)
    return path


def _regular_file(path: Path, root: Path, label: str) -> Path:
    path = _confined(path, root, label, must_exist=True)
    if path.is_symlink() or not path.is_file():
        raise CandidateError(f"{label} must be a regular non-symlink file")
    return path


def _run(command: list[str], *, source_root: Path) -> None:
    subprocess.run(command, cwd=str(source_root), check=True)


def _desktop_helper(source_root: Path) -> Path:
    helper = source_root / ".github" / "scripts" / "desktop_build.py"
    if helper.is_symlink() or not helper.is_file():
        raise CandidateError("desktop build helper is missing or symlinked")
    return helper


def _android_helper(source_root: Path) -> Path:
    helper = source_root / ".github" / "scripts" / "android_build_driver.sh"
    if helper.is_symlink() or not helper.is_file() or not os.access(helper, os.X_OK):
        raise CandidateError("Android build driver is missing or not executable")
    return helper


def _build_desktop(
    source_root: Path,
    platform: str,
    architecture: str,
    skip_deps: bool,
    gradle_bin: Path | None,
) -> None:
    """Use desktop_build.py for both native libraries and the JVM app."""
    helper = _desktop_helper(source_root)
    common = [sys.executable, str(helper)]
    libs = [*common, "libs", "--platform", platform, "--arch", architecture]
    app = [
        *common,
        "app",
        "--platform",
        platform,
        "--arch",
        architecture,
        "--skip-libs",
    ]
    if skip_deps:
        libs.append("--skip-deps")
        app.append("--skip-deps")
    if gradle_bin is not None:
        app.extend(("--gradle-bin", str(gradle_bin)))
    _run(libs, source_root=source_root)
    _run(app, source_root=source_root)


def _build_android(
    source_root: Path,
    candidate_root: Path,
    architecture: str,
    source_sha: str | None,
) -> Path:
    helper = _android_helper(source_root)
    output = candidate_root / "dobbyvpn-release-unsigned.apk"
    manifest = candidate_root / "android-build-manifest.json"
    first_output = candidate_root / "android-build-first.apk"
    reproducibility = candidate_root / "android-reproducibility.json"
    command = [
        str(helper),
        "--source-root",
        str(source_root),
        "--output",
        str(output),
        "--manifest",
        str(manifest),
        "--first-output",
        str(first_output),
        "--reproducibility",
        str(reproducibility),
    ]
    # The Android driver accepts the optional source identity as the strict
    # checkout proof.  Omitting it is what permits a dirty local worktree.
    if source_sha is not None:
        command.extend(("--source-sha", source_sha))
    _run(command, source_root=source_root)
    return output


def _find_desktop_app(source_root: Path, request_root: Path) -> Path:
    build_root = source_root / "kmp_module" / "app" / "build"
    candidates = sorted(
        path
        for path in build_root.rglob("*.jar")
        if path.is_file()
        and not path.is_symlink()
        and not any(part in {"sources", "javadoc"} for part in path.parts)
        and "plain" not in path.stem
    ) if build_root.is_dir() else []
    preferred = [
        path
        for path in candidates
        if path.parent.name == "jars" and "app" in path.stem and "jvm" in path.stem
    ]
    if len(preferred) == 1:
        return _regular_file(preferred[0], request_root, "desktop app")
    if len(candidates) == 1:
        return _regular_file(candidates[0], request_root, "desktop app")
    raise CandidateError("desktop JVM app build did not produce one identifiable app jar")


def _interface(path: Path, process_identity: str, request_root: Path) -> dict[str, str]:
    if not IDENTITY.fullmatch(process_identity):
        raise CandidateError("process identity is invalid")
    return {"path": str(_regular_file(path, request_root, "candidate interface")), "process_identity": process_identity}


def _optional_interface(
    path: Path | None,
    process_identity: str | None,
    request_root: Path,
) -> dict[str, str] | None:
    if path is None or process_identity is None:
        if path is not None or process_identity is not None:
            raise CandidateError("optional interface is incomplete")
        return None
    return _interface(path, process_identity, request_root)


def _descriptor(
    *,
    request_root: Path,
    source_root: Path,
    candidate_root: Path,
    platform: str,
    architecture: str,
    app_path: Path,
    cli_path: Path | None,
    service_path: Path | None,
) -> dict[str, Any]:
    app_identity = ANDROID_APP_IDENTITY if platform == "android" else APP_IDENTITY
    service_identity = SERVICE_IDENTITIES.get(platform)
    network_path = candidate_root / "network-control"
    # The service creates a Unix socket at this path when it starts. Keep it
    # absent so the descriptor can be consumed directly by the runner; on
    # Windows the same confined path is the disposable control-state slot.
    _confined(network_path, request_root, "network interface")
    app_log = _confined(candidate_root / "logs" / "app.log", request_root, "app log", must_exist=True)
    service_log = _confined(candidate_root / "logs" / "service.log", request_root, "service log", must_exist=True)
    result: dict[str, Any] = {
        "schema": SCHEMA,
        "kind": KIND,
        "platform": platform,
        "architecture": architecture,
        "request_root": str(request_root),
        "source_root": str(_confined(source_root, request_root, "source root", must_exist=True)),
        "candidate_root": str(_confined(candidate_root, request_root, "candidate root", must_exist=True)),
        "interfaces": {
            "cli": _optional_interface(
                cli_path,
                CLI_NAMES.get(platform),
                request_root,
            ),
            "service": _optional_interface(service_path, service_identity, request_root),
            "app": _interface(app_path, app_identity, request_root),
            "logs": {
                "app_path": str(_regular_file(app_log, request_root, "app log")),
                "service_path": str(_regular_file(service_log, request_root, "service log")),
            },
            "network": {
                "path": str(_confined(network_path, request_root, "network interface")),
                "process_identity": service_identity or app_identity,
            },
        },
    }
    validate_descriptor(result, request_root)
    return result


def validate_descriptor(document: Any, request_root: Path) -> None:
    """Validate the exact local descriptor schema and its path confinement."""
    if not isinstance(document, dict):
        raise CandidateError("descriptor must be an object")
    expected = {
        "schema", "kind", "platform", "architecture", "request_root", "source_root",
        "candidate_root", "interfaces",
    }
    if set(document) != expected:
        raise CandidateError("descriptor contains unexpected or missing fields")
    if document["schema"] != SCHEMA or document["kind"] != KIND:
        raise CandidateError("descriptor schema identity is invalid")
    platform = document["platform"]
    if platform not in PLATFORMS or not isinstance(document["architecture"], str):
        raise CandidateError("descriptor platform or architecture is invalid")
    root = _existing_directory(request_root, "request root")

    paths = [document[key] for key in ("request_root", "source_root", "candidate_root")]
    if any(not isinstance(path, str) for path in paths):
        raise CandidateError("descriptor roots must be paths")
    if Path(document["request_root"]).resolve(strict=True) != root:
        raise CandidateError("descriptor request root is incorrect")
    for name in ("source_root", "candidate_root"):
        candidate = _confined(Path(document[name]), root, name, must_exist=True)
        if not candidate.is_dir() or candidate.is_symlink():
            raise CandidateError(f"descriptor {name} is not a directory")

    interfaces = document["interfaces"]
    if not isinstance(interfaces, dict) or set(interfaces) != {"cli", "service", "app", "logs", "network"}:
        raise CandidateError("descriptor interfaces are invalid")
    for name in ("cli", "service", "app"):
        value = interfaces[name]
        if value is None:
            if name == "app":
                raise CandidateError("descriptor app interface is required")
            continue
        if not isinstance(value, dict) or set(value) != {"path", "process_identity"}:
            raise CandidateError(f"descriptor {name} interface is invalid")
        if not isinstance(value["path"], str) or not isinstance(value["process_identity"], str):
            raise CandidateError(f"descriptor {name} interface types are invalid")
        _regular_file(Path(value["path"]), root, f"descriptor {name} interface")
        if not IDENTITY.fullmatch(value["process_identity"]):
            raise CandidateError(f"descriptor {name} process identity is invalid")
    logs = interfaces["logs"]
    if not isinstance(logs, dict) or set(logs) != {"app_path", "service_path"}:
        raise CandidateError("descriptor logs are invalid")
    for name in ("app_path", "service_path"):
        if not isinstance(logs[name], str):
            raise CandidateError("descriptor log path is invalid")
        _regular_file(Path(logs[name]), root, "descriptor log")
    network = interfaces["network"]
    if not isinstance(network, dict) or set(network) != {"path", "process_identity"}:
        raise CandidateError("descriptor network interface is invalid")
    if not isinstance(network["path"], str) or not isinstance(network["process_identity"], str):
        raise CandidateError("descriptor network interface types are invalid")
    _confined(Path(network["path"]), root, "descriptor network interface")
    if not IDENTITY.fullmatch(network["process_identity"]):
        raise CandidateError("descriptor network process identity is invalid")


def _write_descriptor(path: Path, request_root: Path, document: dict[str, Any]) -> None:
    path = _confined(path, request_root, "descriptor")
    if path.exists() or path.is_symlink():
        raise CandidateError("descriptor path is already occupied")
    payload = (json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
    try:
        descriptor = os.open(
            path,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0),
            0o600,
        )
        with os.fdopen(descriptor, "wb") as stream:
            descriptor = -1
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
    except OSError as error:
        if "descriptor" in locals() and descriptor != -1:
            os.close(descriptor)
        raise CandidateError(f"could not write descriptor: {error}") from error


def prepare_candidate(
    *,
    request_root: Path,
    source_root: Path,
    platform: str,
    output: Path,
    architecture: str | None = None,
    candidate_root: Path | None = None,
    skip_deps: bool = False,
    source_sha: str | None = None,
    gradle_bin: Path | None = None,
) -> dict[str, Any]:
    request_root = _existing_directory(request_root, "request root")
    source_root = _existing_directory(source_root, "source root")
    # Android's driver confines all of its output to the source checkout.  A
    # shared candidate root below source keeps this contract for every target.
    if not source_root.is_relative_to(request_root):
        raise CandidateError("source root must be below request root")
    if platform not in PLATFORMS:
        raise CandidateError(f"unsupported platform: {platform}")
    architecture = architecture or DEFAULT_ARCHITECTURES[platform]
    if not IDENTITY.fullmatch(architecture):
        raise CandidateError("architecture is invalid")
    if source_sha is not None and (not isinstance(source_sha, str) or not SOURCE_SHA.fullmatch(source_sha)):
        raise CandidateError("source SHA must be a full lowercase Git commit identity")
    if gradle_bin is not None:
        gradle_bin = _confined(Path(gradle_bin), request_root, "Gradle executable", must_exist=True)
        if gradle_bin.is_symlink() or not gradle_bin.is_file() or not os.access(gradle_bin, os.X_OK):
            raise CandidateError("Gradle executable must be a regular executable")

    if candidate_root is None:
        candidate_root = source_root / ".dobbyvpn-local-candidate"
    candidate_root = _confined(Path(candidate_root), request_root, "candidate root")
    if not candidate_root.is_relative_to(source_root):
        raise CandidateError("candidate root must be below source root")
    candidate_root = _new_directory(candidate_root, request_root, "candidate root")
    output = _confined(Path(output), request_root, "descriptor")
    if output.exists() or output.is_symlink():
        raise CandidateError("descriptor path is already occupied")
    logs = candidate_root / "logs"
    try:
        logs.mkdir(mode=0o700)
    except OSError as error:
        raise CandidateError(f"could not create log directory: {error}") from error
    app_log = _new_file(logs / "app.log", request_root, "app log")
    service_log = _new_file(logs / "service.log", request_root, "service log")
    del app_log, service_log

    if platform in DESKTOP_PLATFORMS:
        _build_desktop(source_root, platform, architecture, skip_deps, gradle_bin)
        service_path = source_root / "go_module" / SERVICE_NAMES[platform]
        cli_path = source_root / "go_module" / CLI_NAMES[platform]
        app_path = _find_desktop_app(source_root, request_root)
    else:
        app_path = _build_android(source_root, candidate_root, architecture, source_sha)
        service_path = None
        cli_path = None

    descriptor = _descriptor(
        request_root=request_root,
        source_root=source_root,
        candidate_root=candidate_root,
        platform=platform,
        architecture=architecture,
        app_path=app_path,
        cli_path=cli_path,
        service_path=service_path,
    )
    _write_descriptor(output, request_root, descriptor)
    return descriptor


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Prepare a local DobbyVPN candidate descriptor.")
    parser.add_argument("prepare", choices=("prepare",), help="build and describe one candidate")
    parser.add_argument("--request-root", type=Path, required=True)
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--platform", choices=PLATFORMS, required=True)
    parser.add_argument("--architecture", type=str)
    parser.add_argument("--candidate-root", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--skip-deps", action="store_true")
    parser.add_argument("--source-sha", type=str)
    parser.add_argument("--gradle-bin", type=Path)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        descriptor = prepare_candidate(
            request_root=args.request_root,
            source_root=args.source_root,
            platform=args.platform,
            output=args.output,
            architecture=args.architecture,
            candidate_root=args.candidate_root,
            skip_deps=args.skip_deps,
            source_sha=args.source_sha,
            gradle_bin=args.gradle_bin,
        )
    except CandidateError as error:
        print(f"local candidate rejected: {error}", file=sys.stderr)
        return 2
    print(json.dumps(descriptor, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
