#!/usr/bin/env python3
"""Build one local candidate and return its validated paths."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import platform as host_platform
import re
import subprocess
import sys
import time
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent / "android"))
from android_apk_signing import find_android_tool, sign_test_pair


PLATFORMS = ("linux", "windows", "android", "macos")
DESKTOP_PLATFORMS = ("linux", "windows", "macos")
DEFAULT_ARCHITECTURES = {
    "linux": "amd64",
    "windows": "amd64",
    "android": "arm64-v8a",
    "macos": "arm64",
}
SERVICE_NAMES = {
    "linux": "dobbyvpn-backend",
    "windows": "dobbyvpn-backend.exe",
    "macos": "dobbyvpn-backend",
}
CLI_NAMES = {
    "linux": "dobby-cli",
    "windows": "dobby-cli.exe",
    "macos": "dobby-cli",
}
UI_NAMES = {
    "windows": "DobbyVPN.exe",
    "macos": "Dobby VPN.app",
}
ARCHITECTURE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*\Z")


class CandidateError(ValueError):
    """Raised when a candidate request or one of its paths is invalid."""


@dataclass(frozen=True)
class CandidatePaths:
    mode: str = "local-build"
    app: Path | None = None
    test_companion: Path | None = None
    service: Path | None = None
    cli: Path | None = None
    ui: Path | None = None
    network: Path | None = None

    def to_dict(self) -> dict[str, str]:
        """Render the validated build mode and artifact paths for platform state."""
        return {
            "mode": self.mode,
            **{
                name: str(path)
                for name, path in (
                    ("app", self.app),
                    ("test_companion", self.test_companion),
                    ("service", self.service),
                    ("cli", self.cli),
                    ("ui", self.ui),
                    ("network", self.network),
                )
                if path is not None
            },
        }


def _retain_captured_stream(stream: Any, output: bytes) -> None:
    if not output:
        return
    stream.buffer.write(output)
    stream.buffer.flush()


def _utc_timestamp() -> str:
    timestamp = datetime.now(timezone.utc).isoformat(timespec="milliseconds")
    return timestamp.replace("+00:00", "Z")


def _retain_command_event(
    event: str,
    *,
    label: str,
    status: str,
    returncode: int | None,
    duration_seconds: float | None = None,
) -> None:
    record: dict[str, object] = {
        "event": event,
        "label": label,
        "returncode": returncode,
        "status": status,
        "timestamp_utc": _utc_timestamp(),
    }
    if duration_seconds is not None:
        record["duration_seconds"] = round(duration_seconds, 3)
    _retain_captured_stream(
        sys.stderr,
        (
            "local-candidate-progress "
            + json.dumps(record, sort_keys=True, separators=(",", ":"))
            + "\n"
        ).encode("utf-8"),
    )


def _existing_directory(path: Path, label: str) -> Path:
    path = path.resolve(strict=True)
    if not path.is_dir():
        raise CandidateError(f"{label} must be a directory")
    return path


def _confined(path: Path, root: Path, label: str) -> Path:
    """Resolve one path below the request root."""
    root = root.resolve(strict=True)
    path = path.resolve()
    try:
        path.relative_to(root)
    except ValueError as error:
        raise CandidateError(f"{label} must be below request root") from error
    return path


def _new_directory(path: Path, root: Path, label: str) -> Path:
    path = _confined(path, root, label)
    try:
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
    except OSError as error:
        raise CandidateError(f"could not create {label}: {error}") from error
    return path


def _regular_file(path: Path, root: Path, label: str) -> Path:
    path = _confined(path, root, label)
    if not path.is_file():
        raise CandidateError(f"{label} must be a file")
    return path


def _run(
    command: list[str],
    *,
    label: str,
    source_root: Path,
    environment: dict[str, str] | None = None,
) -> None:
    started = time.monotonic()
    _retain_command_event(
        "start",
        label=label,
        status="started",
        returncode=None,
    )
    try:
        subprocess.run(
            command,
            cwd=str(source_root),
            env=environment,
            check=True,
        )
    except (OSError, subprocess.CalledProcessError) as error:
        _retain_command_event(
            "finish",
            label=label,
            status=("launch-failed" if isinstance(error, OSError) else "failed"),
            returncode=getattr(error, "returncode", None),
            duration_seconds=time.monotonic() - started,
        )
        raise
    _retain_command_event(
        "finish",
        label=label,
        status="completed",
        returncode=0,
        duration_seconds=time.monotonic() - started,
    )


def _desktop_helper(source_root: Path) -> Path:
    helper = source_root / ".github" / "scripts" / "desktop" / "desktop_build.py"
    if not helper.is_file():
        raise CandidateError("desktop build helper is missing")
    return helper


def _android_helper(source_root: Path) -> Path:
    helper = source_root / ".github" / "scripts" / "android" / "android_build_driver.sh"
    if not helper.is_file():
        raise CandidateError("Android build driver is missing")
    return helper


def _build_desktop(
    source_root: Path,
    candidate_root: Path,
    platform: str,
    architecture: str,
    skip_deps: bool,
) -> None:
    """Build the native service and operator CLI used by local VM checks."""
    helper = _desktop_helper(source_root)
    environment = os.environ.copy()
    common = [sys.executable, str(helper)]
    libs = [*common, "libs", "--platform", platform, "--arch", architecture]
    libs.append("--with-cli")
    if skip_deps:
        libs.append("--skip-deps")
    _run(
        libs,
        label=f"desktop libs build ({platform})",
        source_root=source_root,
        environment=environment,
    )
    if platform in {"windows", "macos"}:
        ui_output = candidate_root / ("frontend" if platform == "windows" else UI_NAMES[platform])
        native_ui = [
            *common,
            "native-ui",
            "--platform",
            "current",
            "--arch",
            architecture,
            "--output",
            str(ui_output),
        ]
        if skip_deps:
            native_ui.append("--skip-deps")
        _run(
            native_ui,
            label=f"desktop native UI build ({platform})",
            source_root=source_root,
            environment=environment,
        )


def _build_android(
    source_root: Path,
    candidate_root: Path,
    architecture: str,
    *,
    source_sha: str | None,
    source_tree: str | None,
) -> Path:
    helper = _android_helper(source_root)
    build_check = source_root / ".github" / "scripts" / "android" / "android_build_check.sh"
    output = candidate_root / "dobbyvpn-release-unsigned.apk"
    companion_output = candidate_root / "dobbyvpn-test-companion-unsigned.apk"
    signed_output = candidate_root / "dobbyvpn-release.apk"
    signed_companion = candidate_root / "dobbyvpn-test-companion.apk"
    driver_command = [
        str(helper),
        "--source-root",
        str(source_root),
        "--output",
        str(output),
        "--test-companion-output",
        str(companion_output),
    ]
    environment = os.environ.copy()
    if source_sha is None and source_tree is None:
        command = [
            str(build_check),
            "--source-root", str(source_root),
            "--output", str(output),
            "--test-companion-output", str(companion_output),
        ]
        label = "Android candidate build and native ABI check"
    else:
        if source_sha is None or source_tree is None:
            raise CandidateError("complete Android build requires both source SHA and source tree")
        version = (source_root / "VERSION").read_text(encoding="utf-8").strip()
        output_base = candidate_root
        manifest = output_base / "android-build-driver-manifest.json"
        first_output = output_base / "android-first-unsigned.apk"
        reproducibility = output_base / "android-reproducibility.json"
        dependency_manifest = output_base / "android-dependency-provenance.json"
        driver_command.extend([
            "--source-sha", source_sha,
            "--source-tree", source_tree,
            "--trusted-archive-source",
            "--source-repository", "DobbyVPN/DobbyVPN",
            "--manifest", str(manifest),
            "--first-output", str(first_output),
            "--reproducibility", str(reproducibility),
            "--dependency-manifest", str(dependency_manifest),
        ])

        # Android lint preflight installs this exact Go version through the
        # shared desktop tool bootstrap. Re-select it in this process because
        # PATH changes made by the preflight subprocess do not propagate here.
        sys.path.insert(0, str(source_root / ".github" / "scripts" / "desktop"))
        import desktop_build

        go_binary = str(desktop_build.install_go(skip_deps=True))

        go_path = Path.home() / "go"
        go_path.mkdir(parents=True, exist_ok=True)
        environment["GO_BIN"] = str(Path(go_binary).resolve())
        environment["GOPATH"] = str(go_path)
        environment["GRADLE_BIN"] = str(source_root / "ui" / "android" / "gradlew")
        label = "Android Release-mode candidate build"
        command = driver_command

    _run(command, label=label, source_root=source_root, environment=environment)
    if not output.is_file():
        raise CandidateError("Android build did not produce the application APK")
    if not companion_output.is_file():
        raise CandidateError("Android build did not produce the test companion APK")

    if source_sha is not None and source_tree is not None:
        provenance = candidate_root / "android-provenance.json"
        version_code_match = re.search(
            r"^versionCode=([1-9][0-9]*)$",
            (source_root / "ui" / "android" / "gradle.properties").read_text(encoding="utf-8"),
            re.MULTILINE,
        )
        if version_code_match is None:
            raise CandidateError("Android versionCode is missing or invalid")
        _run(
            [
                sys.executable,
                str(source_root / ".github" / "scripts" / "android" / "android_apk_signing.py"),
                "create-provenance",
                "--profile", "local-complete",
                "--output", str(provenance),
                "--unsigned-apk", str(output),
                "--test-companion", str(companion_output),
                "--build-driver-manifest", str(manifest),
                "--reproducibility", str(reproducibility),
                "--source-sha", source_sha,
                "--source-tree", source_tree,
                "--source-repository", "DobbyVPN/DobbyVPN",
                "--version-name", version,
                "--version-code", version_code_match.group(1),
            ],
            label="Android unsigned build provenance",
            source_root=source_root,
            environment=environment,
        )
    sign_test_pair(
        output,
        companion_output,
        signed_output,
        signed_companion,
        apksigner=find_android_tool("apksigner", "DOBBYVPN_APKSIGNER"),
        keytool=find_android_tool("keytool", "DOBBYVPN_KEYTOOL"),
    )
    output.unlink()
    companion_output.unlink()
    return signed_output


def _candidate_paths(
    *,
    request_root: Path,
    platform: str,
    app_path: Path | None,
    test_companion_path: Path | None,
    cli_path: Path | None,
    ui_path: Path | None,
    service_path: Path | None,
    network_path: Path,
) -> CandidatePaths:
    """Return the validated paths consumed by the local VM helpers."""
    if platform == "android":
        if app_path is None:
            raise CandidateError("Android application is missing")
        if test_companion_path is None:
            raise CandidateError("Android test companion is missing")
        return CandidatePaths(
            app=_regular_file(app_path, request_root, "Android app"),
            test_companion=_regular_file(
                test_companion_path, request_root, "Android test companion"
            ),
        )

    if service_path is None or cli_path is None:
        raise CandidateError("desktop candidate paths are incomplete")
    service = _regular_file(service_path, request_root, "service")
    cli = _regular_file(cli_path, request_root, "CLI")
    ui: Path | None = None
    if platform in {"windows", "macos"}:
        if ui_path is None:
            raise CandidateError("desktop native UI path is missing")
        if platform == "macos":
            ui_path = _confined(ui_path, request_root, "macOS desktop UI")
            if ui_path.is_symlink() or not ui_path.is_dir() or ui_path.suffix != ".app":
                raise CandidateError("macOS desktop UI must be an application bundle")
        else:
            ui_path = _regular_file(ui_path, request_root, "desktop UI")
        ui = ui_path
    return CandidatePaths(
        service=service,
        cli=cli,
        ui=ui,
        network=_confined(network_path, request_root, "network interface"),
    )


def prepare_candidate(
    *,
    request_root: Path,
    source_root: Path,
    platform: str,
    architecture: str | None = None,
    candidate_root: Path | None = None,
    skip_deps: bool = False,
    source_sha: str | None = None,
    source_tree: str | None = None,
) -> CandidatePaths:
    request_root = _existing_directory(request_root, "request root")
    source_root = _existing_directory(source_root, "source root")
    # Android's driver confines all of its output to the source checkout.  A
    # shared candidate root below source keeps this contract for every target.
    if not source_root.is_relative_to(request_root):
        raise CandidateError("source root must be below request root")
    if platform not in PLATFORMS:
        raise CandidateError(f"unsupported platform: {platform}")
    if (source_sha is None) != (source_tree is None):
        raise CandidateError("Android archived-source build requires both source SHA and source tree")
    if source_sha is not None:
        if platform != "android":
            raise CandidateError("source SHA/tree arguments are supported only for Android candidates")
        if re.fullmatch(r"[0-9a-f]{40}", source_sha) is None or re.fullmatch(r"[0-9a-f]{40}", source_tree or "") is None:
            raise CandidateError("Android source SHA and tree must be full lowercase Git identities")
    # Local macOS candidates must match the native host, including Intel Macs;
    # the release lane's Apple-silicon default is not a local build target.
    architecture = architecture or (
        "amd64" if platform == "macos" and host_platform.machine().lower() in {"x86_64", "amd64"}
        else DEFAULT_ARCHITECTURES[platform]
    )
    if not ARCHITECTURE.fullmatch(architecture):
        raise CandidateError("architecture is invalid")
    if candidate_root is None:
        candidate_root = source_root / ".dobbyvpn-local-candidate"
    candidate_root = _confined(Path(candidate_root), request_root, "candidate root")
    if not candidate_root.is_relative_to(source_root):
        raise CandidateError("candidate root must be below source root")
    candidate_root = _new_directory(candidate_root, request_root, "candidate root")
    test_companion_path: Path | None = None
    ui_path: Path | None = None
    if platform in DESKTOP_PLATFORMS:
        _build_desktop(
            source_root,
            candidate_root,
            platform,
            architecture,
            skip_deps,
        )
        service_path = source_root / "core" / SERVICE_NAMES[platform]
        cli_path = source_root / "core" / CLI_NAMES[platform]
        ui_path = (
            candidate_root / "frontend" / UI_NAMES[platform]
            if platform == "windows"
            else candidate_root / UI_NAMES[platform]
            if platform == "macos"
            else None
        )
        app_path = None
    else:
        app_path = _build_android(
            source_root,
            candidate_root,
            architecture,
            source_sha=source_sha,
            source_tree=source_tree,
        )
        test_companion_path = candidate_root / "dobbyvpn-test-companion.apk"
        service_path = None
        cli_path = None

    # Linux Unix-domain socket paths are commonly limited to 107 usable bytes.
    # Linux's request/source path reached 113 bytes and bind returned EINVAL.
    # Keep its runtime beside (not inside) source to fit the real socket limit.
    network_root = _new_directory(
        (source_root.parent if platform == "linux" else source_root) / ".dobbyvpn-run",
        request_root,
        "candidate runtime directory",
    )
    network_path = network_root / "s"

    return _candidate_paths(
        request_root=request_root,
        platform=platform,
        app_path=app_path,
        test_companion_path=test_companion_path,
        cli_path=cli_path,
        ui_path=ui_path,
        service_path=service_path,
        network_path=network_path,
    )
