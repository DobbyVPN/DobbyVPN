"""Run one bounded DobbyVPN qualification candidate in a prepared VM.

The private Harness owns the VM, SSH session, timeout supervisor, and process
tree kill.  This module owns only the candidate inside that VM: build it with
``local_candidate.py``, install/start it, run the canonical functional suite,
and later clean up the exact state recorded in ``platform.json``.

``run`` intentionally leaves the candidate running.  A supervisor invokes
``cleanup`` in a separate step, which also makes a failed setup inspectable.
The run directory is the only state boundary:

    source/       complete DobbyVPN checkout
    profile       private test profile (never copied or printed)
    logs/          command, product, and functional output
    platform.json platform-owned process/install state
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform as host_platform
import re
import socket
import stat
import subprocess
import sys
import time
from typing import Any

PLATFORMS = ("linux", "windows", "macos", "android", "ios-simulator")
SUITES = ("mini", "full")
DESKTOP_PLATFORMS = frozenset(("linux", "windows", "macos"))
_PID = re.compile(r"^[1-9][0-9]*$")
_IDENTITY = re.compile(r"^[A-Za-z0-9._-]+$")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
_RELEASE_REPOSITORY = "DobbyVPN/DobbyVPN"
_RELEASE_WORKFLOW = ".github/workflows/release.yml"

# A desktop full lane is supervised by the VM worker's task deadline.  Give
# the hosted journey an inner deadline so it can emit its failure JSON and
# leave enough time for the exact-process/task cleanup boundary to run.  The
# hosted journey has its own reserve before it waits on the real-window smoke
# process (see hosted/native_ui.py).
_NATIVE_UI_TASK_TIMEOUT_RESERVE_SECONDS = 90.0
_NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS = 900.0

# The native desktop journey runs in an interactive user session rather than
# the SSH worker's process environment.  Keep only values with a concrete
# runtime purpose here.  In particular, do not let CI credentials, endpoints,
# or arbitrary tool configuration cross the desktop boundary.
_NATIVE_UI_HOST_ENVIRONMENT = frozenset({
    # native_ui_smoke.py resolves macOS helpers (and Windows PowerShell) by
    # name, while the Go UI/CLI use HOME for their user-owned stores.
    "PATH",
    "HOME",
})
_NATIVE_UI_RUNTIME_ENVIRONMENT = {
    "windows": frozenset({
        "HOME",
        "PROGRAMDATA",
        "DOBBYVPN_CONTROL_ADDRESS",
        "DOBBYVPN_CONTROL_TOKEN_USER",
        "DOBBY_LOG_PATH",
        "DOBBY_LOG_ROOT",
        "DOBBY_LOG_PRECREATED",
        "GODEBUG",
    }),
    "macos": frozenset({
        "HOME",
        "DOBBYVPN_CONTROL_SOCKET",
    }),
}


class LocalVMError(RuntimeError):
    """A bounded local candidate operation failed."""


def _native_ui_environment(platform: str, runtime: dict[str, Any]) -> dict[str, str]:
    """Build the bounded environment for a native desktop journey.

    ``runtime["environment"]`` is already platform-owned state, but still
    filter it here so a malformed or test-supplied state record cannot turn
    this boundary back into an environment pass-through.  Host values are
    deliberately selected by name; the user's complete SSH/CI environment is
    never copied into the interactive desktop session.
    """

    runtime_names = _NATIVE_UI_RUNTIME_ENVIRONMENT.get(platform)
    if runtime_names is None:
        raise LocalVMError(f"native GUI qualification is unsupported on {platform}")
    environment = {
        name: value
        for name in _NATIVE_UI_HOST_ENVIRONMENT
        if isinstance((value := os.environ.get(name)), str)
    }
    runtime_environment = runtime.get("environment")
    if isinstance(runtime_environment, dict):
        environment.update({
            name: runtime_environment[name]
            for name in runtime_names
            if isinstance(runtime_environment.get(name), str)
        })
    return environment


def _prepare_desktop_ui_home(run_dir: Path) -> str:
    """Give each desktop UI phase an isolated user store and log history."""

    home = run_dir / "ui-home"
    home.mkdir(mode=0o700, parents=True, exist_ok=True)
    home.chmod(0o700)
    return str(home)


def _positive_timeout(value: str) -> float:
    try:
        result = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("timeout must be a number") from error
    if not 0 < result < float("inf"):
        raise argparse.ArgumentTypeError("timeout must be positive and finite")
    return result


def _native_ui_driver_timeout(task_timeout: float) -> float:
    """Return the inner hosted-journey timeout below the task deadline.

    Keep a proportional reserve for short diagnostic invocations while using
    a fixed 90-second reserve for the normal desktop lane.  The result is
    always positive and strictly smaller than the task timeout, so the task
    wrapper can observe the driver's diagnostic result before it terminates
    an unresponsive full run.
    """

    if task_timeout <= 0 or task_timeout == float("inf"):
        raise ValueError("native UI task timeout must be positive and finite")
    reserve = (
        _NATIVE_UI_TASK_TIMEOUT_RESERVE_SECONDS
        if task_timeout > _NATIVE_UI_TASK_TIMEOUT_RESERVE_SECONDS
        else task_timeout / 3.0
    )
    return min(_NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS, task_timeout - reserve)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    for action in ("run", "cleanup"):
        command = commands.add_parser(action)
        command.add_argument("--platform", choices=PLATFORMS, required=True)
        command.add_argument(
            "--run-dir", type=_absolute_path, required=True,
            help="absolute disposable candidate directory",
        )
        command.add_argument("--timeout", type=_positive_timeout, required=True)
        command.add_argument("--suite", choices=SUITES, default="mini")
        command.add_argument("--scenario", action="append", dest="scenarios")
        if action == "run":
            command.add_argument("--architecture")
            command.add_argument("--skip-deps", action="store_true")
            command.add_argument("--network-interface")
            command.add_argument(
                "--release-manifest", type=_absolute_path,
                help="owner-verified exact Release package manifest",
            )
    return parser


def _absolute_path(value: str) -> Path:
    path = Path(value)
    if not path.is_absolute():
        raise argparse.ArgumentTypeError("run-dir must be an absolute path")
    return path


def _run_dir(path: Path) -> Path:
    path = path.resolve()
    if not path.is_absolute() or not path.is_dir():
        raise LocalVMError("run-dir must be an existing directory")
    return path


def _inside(path: Path, root: Path) -> Path:
    path = path.resolve()
    try:
        path.relative_to(root)
    except ValueError as error:
        raise LocalVMError(f"path is outside run-dir: {path}") from error
    return path


def _required_input(run_dir: Path, name: str, *, directory: bool = False) -> Path:
    raw = run_dir / name
    if raw.is_symlink():
        raise LocalVMError(f"run-dir/{name} must not be a symlink")
    path = _inside(raw, run_dir)
    if not path.is_dir() if directory else not path.is_file():
        kind = "directory" if directory else "file"
        raise LocalVMError(f"run-dir/{name} must be a {kind}")
    return path


def _write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.tmp")
    temporary.write_text(
        json.dumps(value, sort_keys=True, indent=2) + "\n", encoding="utf-8"
    )
    temporary.replace(path)


def _read_state(run_dir: Path) -> dict[str, Any] | None:
    path = run_dir / "platform.json"
    if not path.is_file():
        return None
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise LocalVMError(f"platform state is unreadable: {path}") from error
    if not isinstance(value, dict):
        raise LocalVMError("platform state is not an object")
    return value


def _command_log(logs: Path, label: str, stream: str) -> Path:
    if not _IDENTITY.fullmatch(label):
        raise LocalVMError("command label is invalid")
    return logs / f"{label}.{stream}.log"


def _run_logged(
    command: list[str],
    *,
    cwd: Path,
    logs: Path,
    label: str,
    timeout: float,
    environment: dict[str, str] | None = None,
    input_data: bytes | None = None,
    check: bool = True,
) -> subprocess.CompletedProcess[bytes]:
    if not command or any(not isinstance(item, str) or not item for item in command):
        raise LocalVMError(f"{label}: invalid command")
    logs.mkdir(parents=True, exist_ok=True)
    stdout_path = _command_log(logs, label, "stdout")
    stderr_path = _command_log(logs, label, "stderr")
    try:
        # Stream directly to disk so a supervisor timeout still retains the
        # bytes emitted before termination.  The small CompletedProcess below
        # is reconstructed from those same files for callers that need to
        # parse a probe response (PID, route, or launchd record).
        with stdout_path.open("wb") as stdout_stream, stderr_path.open("wb") as stderr_stream:
            run_kwargs: dict[str, Any] = {
                "cwd": str(cwd),
                "env": environment,
                "stdout": stdout_stream,
                "stderr": stderr_stream,
                "timeout": timeout,
                "check": False,
            }
            if input_data is None:
                run_kwargs["stdin"] = subprocess.DEVNULL
            else:
                run_kwargs["input"] = input_data
            completed = subprocess.run(command, **run_kwargs)
    except subprocess.TimeoutExpired as error:
        raise LocalVMError(f"{label}: command timed out") from error
    stdout = stdout_path.read_bytes()
    stderr = stderr_path.read_bytes()
    completed = subprocess.CompletedProcess(
        completed.args, completed.returncode, stdout, stderr
    )
    if check and completed.returncode != 0:
        raise LocalVMError(f"{label}: command exited {completed.returncode}")
    return completed


def _descriptor(run_dir: Path) -> dict[str, Any]:
    path = run_dir / "candidate.json"
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise LocalVMError("local candidate descriptor is unreadable") from error
    if not isinstance(value, dict):
        raise LocalVMError("local candidate descriptor is invalid")
    return value


def _candidate_path(
    descriptor: dict[str, Any], name: str, *, required: bool = True
) -> Path | None:
    value = descriptor.get(name)
    if value is None:
        if required:
            raise LocalVMError(f"candidate path is missing: {name}")
        return None
    if not isinstance(value, str):
        raise LocalVMError(f"candidate path is invalid: {name}")
    return Path(value)


def _file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(block)
    except OSError as error:
        raise LocalVMError(f"cannot read Release artifact: {path}") from error
    return digest.hexdigest()


def _release_document(path: Path, *, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, ValueError) as error:
        raise LocalVMError(f"{label} is unreadable") from error
    if not isinstance(value, dict):
        raise LocalVMError(f"{label} is not an object")
    return value


def _release_artifact_map(run_dir: Path, manifest: dict[str, Any], platform: str) -> dict[tuple[str, str], Path]:
    """Validate the owner manifest and every staged artifact before install."""
    if manifest.get("schema") != 1 or manifest.get("mode") != "release-package":
        raise LocalVMError("Release manifest schema or mode is invalid")
    if manifest.get("repository") != _RELEASE_REPOSITORY:
        raise LocalVMError("Release manifest repository is invalid")
    if manifest.get("workflow") != "Release" or manifest.get("workflow_path") != _RELEASE_WORKFLOW:
        raise LocalVMError("Release manifest workflow is invalid")
    if manifest.get("run_attempt") != 1 or manifest.get("branch") != "main":
        raise LocalVMError("Release manifest is not a first-attempt main-branch run")
    run_id = manifest.get("run_id")
    source_sha = manifest.get("source_sha")
    if not isinstance(run_id, int) or isinstance(run_id, bool) or run_id <= 0:
        raise LocalVMError("Release manifest run ID is invalid")
    if not isinstance(source_sha, str) or _SOURCE_SHA.fullmatch(source_sha) is None:
        raise LocalVMError("Release manifest source SHA is invalid")
    if manifest.get("platform") != platform:
        raise LocalVMError("Release manifest platform does not match this guest")
    raw_artifacts = manifest.get("artifacts")
    if not isinstance(raw_artifacts, list) or not raw_artifacts:
        raise LocalVMError("Release manifest artifact list is invalid")
    expected: dict[tuple[str, str], tuple[str, str, str]]
    if platform == "windows":
        expected = {
            ("package", "amd64"): (
                "dobbyVPN-windows-amd64.msi",
                "dobbyVPN-windows-amd64.msi",
                "release/windows/dobbyVPN-windows-amd64.msi",
            ),
            ("ui-test", "amd64"): (
                "dobby-vpn-ui-test-windows",
                "dobby-vpn-ui-test.exe",
                "release/windows/dobby-vpn-ui-test.exe",
            ),
        }
    elif platform == "macos":
        expected = {
            ("package", "arm64"): (
                "dobbyVPN-macos-aarch64.pkg",
                "dobbyVPN-macos-aarch64.pkg",
                "release/arm64/dobbyVPN-macos-aarch64.pkg",
            ),
            ("package", "amd64"): (
                "dobbyVPN-macos-amd64.pkg",
                "dobbyVPN-macos-amd64.pkg",
                "release/amd64/dobbyVPN-macos-amd64.pkg",
            ),
            ("ui-test", "arm64"): (
                "dobby-vpn-ui-test-macos-arm64",
                "dobby-vpn-ui-test",
                "release/arm64/dobby-vpn-ui-test",
            ),
            ("ui-test", "amd64"): (
                "dobby-vpn-ui-test-macos-amd64",
                "dobby-vpn-ui-test",
                "release/amd64/dobby-vpn-ui-test",
            ),
        }
    else:
        raise LocalVMError("exact Release packages are supported only on Windows/macOS")
    observed: dict[tuple[str, str], Path] = {}
    for item in raw_artifacts:
        if not isinstance(item, dict):
            raise LocalVMError("Release manifest artifact entry is invalid")
        role = item.get("role")
        architecture = item.get("architecture")
        key = (role, architecture)
        if key not in expected or key in observed:
            raise LocalVMError("Release manifest artifact set is missing, extra, or ambiguous")
        artifact_name, file_name, expected_relative = expected[key]
        if (
            item.get("artifact_name") != artifact_name
            or item.get("file_name") != file_name
        ):
            raise LocalVMError("Release manifest artifact identity is invalid")
        relative = item.get("relative_path")
        digest = item.get("sha256")
        if relative != expected_relative:
            raise LocalVMError("Release artifact staging path is invalid")
        if not isinstance(digest, str) or _SHA256.fullmatch(digest) is None:
            raise LocalVMError("Release artifact hash is invalid")
        staged = run_dir / relative
        if staged.is_symlink():
            raise LocalVMError(f"staged Release artifact is a symlink: {relative}")
        path = _inside(staged, run_dir)
        if path.name != file_name or not path.is_file():
            raise LocalVMError(f"staged Release artifact is invalid: {relative}")
        if _file_sha256(path) != digest:
            raise LocalVMError(f"Release artifact hash mismatch: {file_name}")
        if platform == "macos" and role == "ui-test":
            try:
                # scp does not promise to preserve the source executable bit.
                # Apply the guest-local mode only after hashing the bytes, then
                # require the companion to be owner-only and executable.
                path.chmod(0o700)
                mode = path.stat()
            except OSError as error:
                raise LocalVMError("could not prepare macOS UI companion") from error
            if mode.st_uid != os.getuid() or stat.S_IMODE(mode.st_mode) != 0o700:
                raise LocalVMError("macOS UI companion ownership or mode is invalid")
            if not os.access(path, os.X_OK):
                raise LocalVMError("macOS UI companion is not executable")
        observed[key] = path
    if set(observed) != set(expected):
        raise LocalVMError("Release manifest artifact set is incomplete")
    return observed


def _validate_release_inputs(run_dir: Path, source: Path, manifest_path: Path) -> tuple[dict[str, Any], dict[tuple[str, str], Path]]:
    if manifest_path.is_symlink():
        raise LocalVMError("Release manifest must be a regular file")
    manifest_path = _inside(manifest_path, run_dir)
    manifest = _release_document(manifest_path, label="Release manifest")
    identity = _release_document(_required_input(run_dir, "source-identity.json"), label="source identity")
    if identity.get("schema") != 1 or identity.get("repository") != _RELEASE_REPOSITORY:
        raise LocalVMError("source identity is invalid")
    if identity.get("source_sha") != manifest.get("source_sha"):
        raise LocalVMError("source identity and Release manifest source SHA differ")
    if identity.get("release_run_id") != manifest.get("run_id"):
        raise LocalVMError("source identity and Release manifest run ID differ")
    if identity.get("platform") != manifest.get("platform"):
        raise LocalVMError("source identity and Release manifest platform differ")
    git_dir = source / ".git"
    if git_dir.exists():
        try:
            result = subprocess.run(
                ["git", "-C", str(source), "rev-parse", "--verify", "HEAD"],
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                check=False, timeout=30,
            )
        except (OSError, subprocess.TimeoutExpired) as error:
            raise LocalVMError("could not verify staged source revision") from error
        if result.returncode or result.stdout.decode("utf-8", errors="replace").strip() != manifest["source_sha"]:
            raise LocalVMError("staged source revision does not match Release manifest")
    return manifest, _release_artifact_map(run_dir, manifest, str(manifest.get("platform")))


def _write_candidate_descriptor(run_dir: Path, descriptor: dict[str, Any]) -> dict[str, Any]:
    _write_json(run_dir / "candidate.json", descriptor)
    return descriptor


def _prepare_candidate(run_dir: Path, platform: str, logs: Path, timeout: float, *, architecture: str | None, skip_deps: bool) -> None:
    source = run_dir / "source"
    command = [
        sys.executable, str(source / ".github/scripts/local_candidate.py"),
        "prepare", "--request-root", str(run_dir), "--source-root", str(source),
        "--platform", platform, "--output", str(run_dir / "candidate.json"),
    ]
    if architecture:
        command.extend(("--architecture", architecture))
    if skip_deps:
        command.append("--skip-deps")
    _run_logged(command, cwd=source, logs=logs, label="candidate-prepare", timeout=timeout)


def _discover_network_interface(
    run_dir: Path, logs: Path, timeout: float, platform: str,
    configured: str | None,
) -> str:
    pattern = r"^[A-Za-z0-9_.:-]+$" if platform == "linux" else r"^[A-Za-z0-9._-]+$"
    if configured:
        if re.fullmatch(pattern, configured) is None:
            raise LocalVMError("network interface is invalid")
        return configured
    if platform == "linux":
        result = _run_logged(
            ["ip", "-o", "route", "show", "default"], cwd=run_dir,
            logs=logs, label="network-interface", timeout=timeout,
        )
        match = re.search(r"(?:^|\s)dev\s+([^\s]+)", result.stdout.decode(errors="replace"))
    elif platform == "macos":
        result = _run_logged(
            ["route", "-n", "get", "default"], cwd=run_dir,
            logs=logs, label="network-interface", timeout=timeout,
        )
        match = re.search(r"(?m)^[ \t]*interface:\s*([^\s]+)", result.stdout.decode(errors="replace"))
    else:
        raise LocalVMError(f"network interface discovery is unsupported on {platform}")
    if match is None or re.fullmatch(pattern, match.group(1)) is None:
        raise LocalVMError("network interface could not be discovered")
    return match.group(1)


def _wait_linux_service(pid: int, service: Path, control_socket: Path, timeout: float) -> None:
    deadline = time.monotonic() + min(timeout, 30)
    while time.monotonic() < deadline:
        if _pid_matches(pid, str(service.resolve())) and control_socket.is_socket():
            try:
                with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as probe:
                    probe.settimeout(min(0.2, max(0.01, deadline - time.monotonic())))
                    probe.connect(str(control_socket))
                return
            except OSError:
                pass
        time.sleep(min(0.1, max(0.01, deadline - time.monotonic())))
    raise LocalVMError("Linux service did not become ready")


def _start_linux(
    run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float,
    network_interface: str,
) -> dict[str, Any]:
    service = _candidate_path(descriptor, "service")
    network = _candidate_path(descriptor, "network")
    if service is None or network is None:
        raise LocalVMError("Linux candidate service paths are incomplete")
    logs.mkdir(parents=True, exist_ok=True)
    service_log = logs / "service.log"
    service_log.touch(exist_ok=True)
    pid_file = run_dir / "service.pid"
    pid_file.touch(exist_ok=True)
    # The service runs as root through the guest's existing noninteractive
    # sudo rule.  Keep the SSH account's UID in the control-socket policy so
    # the local functional client remains the authorized peer.
    launch_script = (
        'binary=$1; socket=$2; log=$3; request_root=$4; library=$5; pid_file=$6; peer_uid=$7; log_root=$8; '
        'stdout_path=$9; stderr_path=${10}; '
        'export DOBBYVPN_CONTROL_SOCKET="$socket" DOBBYVPN_CONTROL_PEER_UID="$peer_uid" '
        'DOBBYVPN_SUPERVISED_REQUEST=1 DOBBYVPN_REQUEST_ROOT="$request_root" '
        'DOBBYVPN_SSH_RUN="${request_root##*/}" DOBBY_LOG_PRECREATED=1 DOBBY_LOG_PATH="$log" '
        'DOBBY_LOG_ROOT="$log_root"; '
        'if [ -n "$library" ]; then export LD_LIBRARY_PATH="$library"; fi; '
        'setsid "$binary" >"$stdout_path" 2>"$stderr_path" < /dev/null & '
        'pid=$!; printf "%s\\n" "$pid" > "$pid_file"; printf "%s\\n" "$pid"'
    )
    command = [
        "sudo", "-n", "sh", "-c", launch_script,
        "dobbyvpn-service", str(service), str(network), str(service_log),
        str(run_dir), str(service.parent), str(pid_file), str(os.getuid()), str(logs),
        str(logs / "service.log.stdout"), str(logs / "service.log.stderr"),
    ]
    result = _run_logged(command, cwd=service.parent, logs=logs, label="service-start", timeout=timeout)
    values = result.stdout.decode("ascii", errors="replace").split()
    if not values or not _PID.fullmatch(values[-1]):
        raise LocalVMError("Linux service start returned no service PID")
    pid = int(values[-1])
    (run_dir / "service.pid").write_text(f"{pid}\n", encoding="ascii")
    _wait_linux_service(pid, service, network, timeout)
    return {
        "pid": pid,
        "binary": str(service.resolve()),
        "pid_file": str(run_dir / "service.pid"),
        "socket": str(network),
        "environment": {"DOBBYVPN_CONTROL_SOCKET": str(network)},
        "library_path": str(service.parent),
        "process_group": pid,
        "network_interface": network_interface,
    }


def _start_macos(
    run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float,
    network_interface: str,
) -> dict[str, Any]:
    service = _candidate_path(descriptor, "service")
    network = _candidate_path(descriptor, "network")
    if service is None or network is None:
        raise LocalVMError("macOS candidate service paths are incomplete")
    source = run_dir / "source"
    resources = service.parent
    plist = source / "installer/macos/vpnservice.plist"
    if not plist.is_file():
        raise LocalVMError("macOS service plist is missing")
    logs.mkdir(parents=True, exist_ok=True)
    (logs / "service.log").touch(exist_ok=True)
    # Reinstall the launchd job from this candidate's binary.  This uses the
    # product installer hook and avoids accidentally qualifying an older app
    # left on the VM.
    _run_logged(
        [
            "sudo", "-n", "env",
            f"DOBBYVPN_SERVICE_RESOURCES={resources}",
            f"DOBBYVPN_SERVICE_PLIST={plist}",
            f"DOBBYVPN_CONTROL_PEER_UID={os.getuid()}",
            f"DOBBY_LOG_PATH={logs / 'service.log'}",
            f"DOBBY_SERVICE_STDOUT_PATH={logs / 'service.stdout.log'}",
            f"DOBBY_SERVICE_STDERR_PATH={logs / 'service.stderr.log'}",
            "/bin/bash", str(source / "installer/macos/postinstall.sh"),
        ],
        cwd=run_dir, logs=logs, label="service-start", timeout=timeout,
    )
    result = _run_logged(
        ["launchctl", "print", "system/com.dobby.vpnservice"],
        cwd=run_dir, logs=logs, label="service-probe", timeout=timeout,
    )
    pids = re.findall(r"(?m)^\s*pid\s*=\s*([1-9][0-9]*)\s*$", result.stdout.decode(errors="replace"))
    if len(pids) != 1:
        raise LocalVMError("launchd service PID is missing or ambiguous")
    pid_file = run_dir / "service.pid"
    pid_file.write_text(f"{pids[0]}\n", encoding="ascii")
    return {
        "pid": int(pids[0]),
        "pid_file": str(pid_file),
        "binary": str(service.resolve()),
        "socket": "/var/run/dobbyvpn/control.sock",
        "environment": {"DOBBYVPN_CONTROL_SOCKET": "/var/run/dobbyvpn/control.sock"},
        "launchd_label": "system/com.dobby.vpnservice",
        "plist": "/Library/LaunchDaemons/com.dobby.vpnservice.plist",
        "network_interface": network_interface,
    }


def _start_macos_release(
    run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float,
    network_interface: str,
) -> dict[str, Any]:
    """Observe the launchd service installed by the exact Release package."""
    service = _candidate_path(descriptor, "service")
    if service is None or not service.is_file():
        raise LocalVMError("installed macOS Release service is missing")
    result = _run_logged(
        ["launchctl", "print", "system/com.dobby.vpnservice"],
        cwd=run_dir, logs=logs, label="service-probe", timeout=timeout,
    )
    pids = re.findall(
        r"(?m)^\s*pid\s*=\s*([1-9][0-9]*)\s*$",
        result.stdout.decode(errors="replace"),
    )
    if len(pids) != 1:
        raise LocalVMError("installed macOS launchd service PID is missing or ambiguous")
    pid_file = run_dir / "service.pid"
    pid_file.write_text(f"{pids[0]}\n", encoding="ascii")
    return {
        "pid": int(pids[0]),
        "pid_file": str(pid_file),
        "binary": str(service.resolve()),
        "socket": "/var/run/dobbyvpn/control.sock",
        "environment": {"DOBBYVPN_CONTROL_SOCKET": "/var/run/dobbyvpn/control.sock"},
        "launchd_label": "system/com.dobby.vpnservice",
        "plist": "/Library/LaunchDaemons/com.dobby.vpnservice.plist",
        "network_interface": network_interface,
    }


def _release_state(run_dir: Path, manifest: dict[str, Any], package: Path, architecture: str) -> dict[str, Any]:
    state = _read_state(run_dir) or {"platform": manifest.get("platform"), "suite": "full"}
    release = {
        "mode": "release-package",
        "repository": manifest["repository"],
        "workflow": manifest["workflow"],
        "run_id": manifest["run_id"],
        "source_sha": manifest["source_sha"],
        "platform": manifest["platform"],
        "architecture": architecture,
        "package_path": str(package),
        "package_sha256": _file_sha256(package),
        "install_attempted": False,
        "installed": False,
    }
    state["release"] = release
    state["status"] = "install-pending"
    _write_json(run_dir / "platform.json", state)
    return release


def _install_windows_release(
    run_dir: Path, manifest: dict[str, Any], artifacts: dict[tuple[str, str], Path],
    logs: Path, timeout: float,
) -> tuple[dict[str, Any], dict[str, Any]]:
    package = artifacts[("package", "amd64")]
    ui_test = artifacts[("ui-test", "amd64")]
    release = _release_state(run_dir, manifest, package, "amd64")
    state = _read_state(run_dir) or {}
    release["install_attempted"] = True
    state["release"] = release
    state["status"] = "installing"
    _write_json(run_dir / "platform.json", state)
    package_log = run_dir / "logs" / "windows-install.log"
    try:
        result = _run_logged(
            [
                "msiexec.exe", "/i", str(package), "/qn", "/norestart",
                "/L*v", str(package_log),
            ],
            cwd=run_dir, logs=logs, label="release-install", timeout=timeout,
            check=False,
        )
        release["install_returncode"] = result.returncode
        if result.returncode != 0:
            state["release"] = release
            _write_json(run_dir / "platform.json", state)
            raise LocalVMError(f"exact Windows MSI install exited {result.returncode}")
        release["installed"] = True
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        query = r'''$ErrorActionPreference = "Stop"
$root = Join-Path $env:ProgramFiles "DobbyVPN"
$cli = @(Get-ChildItem $root -Filter "dobby-cli.exe" -Recurse -File)
$service = @(Get-ChildItem $root -Filter "windows_grpcvpnserver.exe" -Recurse -File)
$ui = Join-Path $root "bin\Dobby Vpn.exe"
if ($cli.Count -ne 1 -or $service.Count -ne 1 -or -not (Test-Path -LiteralPath $ui -PathType Leaf)) {
  throw "installed Release closure is ambiguous"
}
Write-Output ("$($cli[0].FullName)|$($service[0].FullName)|$ui")
'''
        paths = _run_logged(
            ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", query],
            cwd=run_dir, logs=logs, label="release-installed-paths", timeout=timeout,
        ).stdout.decode("utf-8", errors="strict").strip().split("|")
        if len(paths) != 3 or any(not value for value in paths):
            raise LocalVMError("installed Windows Release paths are invalid")
        descriptor = {
            "service": paths[1], "cli": paths[0], "ui": paths[2],
            "ui_test": str(ui_test),
            "network": str(run_dir / ".dobbyvpn-run" / "s"),
        }
        release["installed_paths"] = {
            "service": paths[1], "cli": paths[0], "ui": paths[2],
            "ui_test": str(ui_test),
        }
        state["release"] = release
        state["candidate"] = descriptor
        state["status"] = "candidate-prepared"
        _write_json(run_dir / "platform.json", state)
        return descriptor, release
    except Exception as error:
        release["install_error"] = f"{type(error).__name__}: {error}"
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        raise


def _install_macos_release(
    run_dir: Path, manifest: dict[str, Any], artifacts: dict[tuple[str, str], Path],
    logs: Path, timeout: float,
) -> tuple[dict[str, Any], dict[str, Any]]:
    machine = host_platform.machine().lower()
    architecture = "arm64" if machine in {"arm64", "aarch64"} else "amd64" if machine in {"x86_64", "amd64"} else ""
    if not architecture:
        raise LocalVMError(f"unsupported macOS guest architecture: {machine}")
    package = artifacts[("package", architecture)]
    ui_test = artifacts[("ui-test", architecture)]
    release = _release_state(run_dir, manifest, package, architecture)
    state = _read_state(run_dir) or {}
    release["install_attempted"] = True
    state["release"] = release
    state["status"] = "installing"
    _write_json(run_dir / "platform.json", state)
    try:
        result = _run_logged(
            [
                "sudo", "-n", "env",
                f"DOBBYVPN_CONTROL_PEER_UID={os.getuid()}",
                f"DOBBY_LOG_PATH={run_dir / 'logs' / 'service.log'}",
                "installer", "-pkg", str(package), "-target", "/",
            ],
            cwd=run_dir, logs=logs, label="release-install", timeout=timeout,
            check=False,
        )
        release["install_returncode"] = result.returncode
        if result.returncode != 0:
            state["release"] = release
            _write_json(run_dir / "platform.json", state)
            raise LocalVMError(f"exact macOS package install exited {result.returncode}")
        release["installed"] = True
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        paths = {
            "service": Path("/Applications/Dobby VPN.app/Contents/Resources/macos_grpcvpnserver"),
            "cli": Path("/Applications/Dobby VPN.app/Contents/Resources/dobby-cli"),
            "ui": Path("/Applications/Dobby VPN.app/Contents/MacOS/Dobby Vpn"),
        }
        if any(not path.is_file() for path in paths.values()):
            raise LocalVMError("installed macOS Release closure is incomplete")
        descriptor = {
            "service": str(paths["service"]), "cli": str(paths["cli"]),
            "ui": str(paths["ui"]), "ui_test": str(ui_test),
            "network": str(run_dir / ".dobbyvpn-run" / "s"),
        }
        release["installed_paths"] = {key: str(value) for key, value in paths.items()}
        release["installed_paths"]["ui_test"] = str(ui_test)
        state["release"] = release
        state["candidate"] = descriptor
        state["status"] = "candidate-prepared"
        _write_json(run_dir / "platform.json", state)
        return descriptor, release
    except Exception as error:
        release["install_error"] = f"{type(error).__name__}: {error}"
        state["release"] = release
        _write_json(run_dir / "platform.json", state)
        raise


def _prepare_release_candidate(
    run_dir: Path, platform: str, manifest_path: Path, logs: Path, timeout: float,
) -> dict[str, Any]:
    source = _required_input(run_dir, "source", directory=True)
    manifest, artifacts = _validate_release_inputs(run_dir, source, manifest_path)
    if platform == "windows":
        descriptor, _ = _install_windows_release(run_dir, manifest, artifacts, logs, timeout)
    elif platform == "macos":
        descriptor, _ = _install_macos_release(run_dir, manifest, artifacts, logs, timeout)
    else:
        raise LocalVMError("exact Release packages are supported only on Windows/macOS")
    return descriptor


def _start_windows(run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float) -> dict[str, Any]:
    from .local_vm_windows import start
    return start(run_dir, descriptor, logs, timeout)


def _start_android(run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float) -> dict[str, Any]:
    from .local_vm_android import start
    return start(run_dir, descriptor, logs, timeout)


def _start_ios(
    run_dir: Path,
    logs: Path,
    timeout: float,
    architecture: str | None,
) -> dict[str, Any]:
    from .local_vm_ios import run
    return run(run_dir, logs, timeout, architecture)


def _functional_command(
    run_dir: Path,
    descriptor: dict[str, Any],
    platform: str,
    timeout: float,
    scenarios: list[str] | None,
    suite: str,
) -> list[str]:
    logs = run_dir / "logs"
    command = [
        sys.executable, "-m", "torturer_checks.functional", "--platform", platform,
        "--profile", str(run_dir / "profile"), "--output", str(logs / "functional.json"),
        "--raw-log-dir", str(logs), "--platform-version", f"local-{platform}",
        "--lane-timeout-seconds", str(timeout),
        "--suite", suite,
    ]
    if platform == "android":
        command.extend(("--adb", str(descriptor["runtime"]["adb"])))
    else:
        command.extend(("--cli", str(_candidate_path(descriptor, "cli")),))
        if platform in {"windows", "macos"}:
            command.extend(("--ui-test", str(_candidate_path(descriptor, "ui_test")),))
        runtime = descriptor.get("runtime", {})
        for name, flag in (("pid", "--service-pid"), ("binary", "--service-binary"), ("socket", "--service-socket"), ("library_path", "--service-library-path"), ("pid_file", "--service-pid-file"), ("identity_file", "--service-identity-file")):
            if name in runtime:
                command.extend((flag, str(runtime[name])))
        if "network_interface" in runtime:
            command.extend(("--network-interface", str(runtime["network_interface"])))
        if platform == "linux":
            command.extend(("--routing-firewall-helper", str(Path(__file__).resolve().parents[1] / "helpers/local/linux/routing-probe-firewall"),))
        elif platform == "macos":
            helper = Path(__file__).resolve().parents[1] / "helpers/local/macos/network-transition"
            command.extend(("--routing-firewall-helper", str(helper), "--network-transition-helper", str(helper)))
    for scenario in scenarios or []:
        command.extend(("--scenario", scenario))
    return command


def _native_ui_command(
    run_dir: Path,
    descriptor: dict[str, Any],
    runtime: dict[str, Any],
    platform: str,
    timeout: float,
) -> list[str]:
    """Build the real-window journey command for desktop full guests."""
    if platform not in {"windows", "macos"}:
        raise LocalVMError(f"native GUI qualification is unsupported on {platform}")
    smoke = run_dir / "source" / ".github" / "scripts" / "native_ui_smoke.py"
    module = run_dir / "source" / "torturer" / "torturer_checks" / "hosted" / "native_ui.py"
    if not smoke.is_file():
        raise LocalVMError("native desktop UI qualification script is missing")
    if not module.is_file():
        raise LocalVMError("native desktop UI journey module is missing")
    for name in ("cli", "ui"):
        if not isinstance(descriptor.get(name), str):
            raise LocalVMError(f"native desktop UI candidate path is missing: {name}")
    for name in ("pid", "binary", "socket"):
        if name not in runtime:
            raise LocalVMError(f"native desktop UI runtime value is missing: {name}")
    task_timeout = min(timeout, _NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS)
    command = [
        sys.executable,
        "-m",
        "torturer_checks.hosted.native_ui",
        "--platform", platform,
        "--cli", str(descriptor["cli"]),
        "--ui", str(descriptor["ui"]),
        "--profile", str(run_dir / "profile"),
        "--smoke-script", str(smoke),
        "--raw-log-dir", str(run_dir / "logs"),
        "--output", str(run_dir / "logs" / "native-ui.json"),
        "--timeout", str(_native_ui_driver_timeout(task_timeout)),
        "--service-pid", str(runtime["pid"]),
        "--service-binary", str(runtime["binary"]),
        "--service-socket", str(runtime["socket"]),
    ]
    for name, flag in (
        ("library_path", "--service-library-path"),
        ("pid_file", "--service-pid-file"),
        ("identity_file", "--service-identity-file"),
    ):
        if name in runtime and runtime[name] is not None:
            command.extend((flag, str(runtime[name])))
    if runtime.get("network_interface") is not None:
        command.extend(("--network-interface", str(runtime["network_interface"])))
    if platform == "macos":
        helper = Path(__file__).resolve().parents[1] / "helpers/local/macos/network-transition"
        command.extend(("--routing-firewall-helper", str(helper)))
    return command


def _run_native_ui(
    command: list[str],
    *,
    platform: str,
    run_dir: Path,
    cwd: Path,
    logs: Path,
    timeout: float,
    environment: dict[str, str],
) -> subprocess.CompletedProcess[bytes]:
    """Run desktop UI smoke in the platform's actual interactive context."""

    if platform == "windows":
        from .local_vm_windows import run_interactive_ui

        return run_interactive_ui(
            command,
            run_dir=run_dir,
            cwd=cwd,
            logs=logs,
            timeout=timeout,
            environment=environment,
        )
    if platform == "macos":
        from .local_vm_macos import run_interactive_ui

        return run_interactive_ui(
            command,
            run_dir=run_dir,
            cwd=cwd,
            logs=logs,
            timeout=timeout,
            environment=environment,
        )
    return _run_logged(
        command,
        cwd=cwd,
        logs=logs,
        label="native-ui",
        timeout=timeout,
        environment=environment,
        check=False,
    )


def _record_native_ui_unavailable(run_dir: Path, platform: str, error: Exception) -> None:
    """Retain an explicit unavailable native-window result for collection."""

    reason_code = str(getattr(error, "reason_code", "NATIVE_UI_UNAVAILABLE"))
    _write_json(run_dir / "logs" / "native-ui.json", {
        "suite": "full",
        "action_driver": "native-window",
        "platform": platform,
        "complete": False,
        "status": "unavailable",
        "availability": "unavailable",
        "reason_code": reason_code,
        "error": str(error),
    })


def _refresh_desktop_runtime_after_headless(
    runtime: dict[str, Any],
) -> dict[str, Any]:
    """Use the service PID sidecar after headless process-loss recovery.

    Windows and macOS process-loss adapters deliberately replace the service
    and update ``service.pid``.  A desktop full lane starts after mini, so the
    native journey must bind to that current candidate identity rather than
    the PID recorded before mini began.
    """
    pid_file = runtime.get("pid_file")
    if isinstance(pid_file, str):
        try:
            value = Path(pid_file).read_text(encoding="ascii").strip()
        except (OSError, UnicodeDecodeError) as error:
            raise LocalVMError("desktop service PID sidecar is unavailable after mini") from error
        if _PID.fullmatch(value) is None:
            raise LocalVMError("desktop service PID sidecar is invalid after mini")
        runtime = dict(runtime)
        runtime["pid"] = int(value)
    return runtime


def run(args: argparse.Namespace) -> int:
    run_dir = _run_dir(args.run_dir)
    source = _required_input(run_dir, "source", directory=True)
    if args.suite == "full" and args.platform not in {"windows", "macos"}:
        raise LocalVMError(
            f"{args.platform} full is unsupported: local full adds a native desktop window only"
        )
    if args.suite == "full" and args.scenarios:
        raise LocalVMError(
            "full qualification cannot select focused scenarios; run diagnostics with --suite mini"
        )
    if args.release_manifest is not None and (
        args.suite != "full" or args.platform not in {"windows", "macos"}
    ):
        raise LocalVMError("exact Release packages require full Windows/macOS coverage")
    if args.platform != "ios-simulator":
        _required_input(run_dir, "profile")
    logs = run_dir / "logs"
    (run_dir / "output").mkdir(parents=True, exist_ok=True)
    state: dict[str, Any] = {
        "platform": args.platform,
        "suite": args.suite,
        "status": "preparing",
    }
    _write_json(run_dir / "platform.json", state)
    try:
        if args.platform == "ios-simulator":
            runtime = _start_ios(
                run_dir, logs, args.timeout, args.architecture,
            )
            state.update(runtime=runtime, status="functional-complete", functional_exit_code=0)
            _write_json(run_dir / "platform.json", state)
            return 0
        if args.release_manifest is not None:
            descriptor = _prepare_release_candidate(
                run_dir, args.platform, args.release_manifest, logs, args.timeout,
            )
            persisted = _read_state(run_dir)
            if persisted is not None:
                state.update(persisted)
            state["candidate"] = descriptor
            state["status"] = "candidate-prepared"
            _write_json(run_dir / "platform.json", state)
        else:
            _prepare_candidate(run_dir, args.platform, logs, args.timeout, architecture=args.architecture, skip_deps=args.skip_deps)
            descriptor = _descriptor(run_dir)
            state["candidate"] = descriptor
            state["status"] = "candidate-prepared"
            _write_json(run_dir / "platform.json", state)
        # Record the paths and cleanup seam before invoking any native start
        # command.  A supervisor can therefore clean a setup that fails
        # between the first side effect and the returned runtime metadata.
        if args.platform == "linux":
            service = _candidate_path(descriptor, "service")
            network = _candidate_path(descriptor, "network")
            if service is not None and network is not None:
                state["runtime"] = {
                    "binary": str(service.resolve()), "socket": str(network),
                    "pid_file": str(run_dir / "service.pid"),
                }
        elif args.platform == "macos":
            service = _candidate_path(descriptor, "service")
            network = _candidate_path(descriptor, "network")
            if service is not None and network is not None:
                state["runtime"] = {
                    "binary": str(service.resolve()),
                    "socket": "/var/run/dobbyvpn/control.sock",
                    "launchd_label": "system/com.dobby.vpnservice",
                    "plist": "/Library/LaunchDaemons/com.dobby.vpnservice.plist",
                }
        if args.platform in {"linux", "macos"}:
            interface = _discover_network_interface(
                run_dir, logs, args.timeout, args.platform, args.network_interface
            )
            state.setdefault("runtime", {})["network_interface"] = interface
        _write_json(run_dir / "platform.json", state)
        if args.platform == "linux":
            runtime = _start_linux(
                run_dir, descriptor, logs, args.timeout,
                state.get("runtime", {}).get("network_interface"),
            )
        elif args.platform == "macos":
            if args.release_manifest is not None:
                runtime = _start_macos_release(
                    run_dir, descriptor, logs, args.timeout,
                    state.get("runtime", {}).get("network_interface"),
                )
            else:
                runtime = _start_macos(
                    run_dir, descriptor, logs, args.timeout,
                    state.get("runtime", {}).get("network_interface"),
                )
        elif args.platform == "windows":
            if args.release_manifest is not None:
                from .local_vm_windows import stop_installed_service

                release_state = state.get("release")
                if not isinstance(release_state, dict):
                    raise LocalVMError("exact Windows Release install state is missing")
                release_state["msi_service_stop_attempted"] = True
                state["release"] = release_state
                _write_json(run_dir / "platform.json", state)
                try:
                    stop_installed_service(run_dir, logs, args.timeout)
                except Exception as error:
                    release_state["msi_service_stop_error"] = f"{type(error).__name__}: {error}"
                    state["release"] = release_state
                    _write_json(run_dir / "platform.json", state)
                    raise
                release_state["msi_service_stopped"] = True
                state["release"] = release_state
                _write_json(run_dir / "platform.json", state)
            runtime = _start_windows(run_dir, descriptor, logs, args.timeout)
        elif args.platform == "android":
            runtime = _start_android(run_dir, descriptor, logs, args.timeout)
        if args.platform in {"windows", "macos"}:
            runtime_environment = runtime.get("environment")
            runtime_environment = dict(runtime_environment) if isinstance(runtime_environment, dict) else {}
            runtime_environment["HOME"] = _prepare_desktop_ui_home(run_dir)
            runtime["environment"] = runtime_environment
        state["runtime"] = runtime
        state["status"] = "running"
        _write_json(run_dir / "platform.json", state)
        if args.platform == "android":
            from .local_vm_android import run_ui as run_android_ui

            native_result = run_android_ui(
                run_dir,
                runtime,
                logs,
                args.timeout,
            )
            state["native_ui_exit_code"] = native_result.returncode
            if native_result.returncode != 0:
                state["status"] = "native-ui-failed"
                _write_json(run_dir / "platform.json", state)
                return native_result.returncode
            state["native_ui_status"] = "passed"
            _write_json(run_dir / "platform.json", state)
        # Desktop full is cumulative: the canonical headless mini lane runs
        # once first, then the native-window journey runs as a second phase.
        # Windows' hosted adapter closes its Job-owned process when mini
        # finalizes, so start a fresh SYSTEM candidate before the native UI;
        # macOS launchd keeps its replacement alive and can refresh its
        # sidecar PID instead.
        functional_suite = "mini" if args.suite == "full" else args.suite
        command = _functional_command(
            run_dir, {**descriptor, "runtime": runtime}, args.platform,
            args.timeout, args.scenarios, functional_suite,
        )
        functional_environment = {
            **os.environ,
            "PYTHONPATH": str(run_dir / "source" / "torturer"),
            "DOBBYVPN_SSH_RUN": run_dir.name,
        }
        if args.platform == "linux":
            functional_environment.update({
                "DOBBYVPN_SUPERVISED_REQUEST": "1",
                "DOBBYVPN_REQUEST_ROOT": str(run_dir),
            })
        runtime_environment = runtime.get("environment")
        if isinstance(runtime_environment, dict):
            functional_environment.update({
                str(key): str(value)
                for key, value in runtime_environment.items()
                if isinstance(key, str) and isinstance(value, str)
            })
        result = _run_logged(
            command, cwd=run_dir / "source" / "torturer", logs=logs,
            label="functional", timeout=args.timeout,
            environment=functional_environment, check=False,
        )
        state["functional_exit_code"] = result.returncode
        state["status"] = "functional-complete" if result.returncode == 0 else "functional-failed"
        _write_json(run_dir / "platform.json", state)
        if result.returncode != 0:
            return result.returncode
        if args.suite == "full" and args.platform in {"windows", "macos"}:
            if args.platform == "windows":
                runtime = _start_windows(run_dir, descriptor, logs, args.timeout)
            else:
                runtime = _refresh_desktop_runtime_after_headless(runtime)
            runtime_environment = runtime.get("environment")
            runtime_environment = dict(runtime_environment) if isinstance(runtime_environment, dict) else {}
            runtime_environment["HOME"] = _prepare_desktop_ui_home(run_dir)
            runtime["environment"] = runtime_environment
            native_descriptor = descriptor
            if args.platform == "macos":
                # Release candidates already live inside the installed app
                # bundle; local build candidates are naked binaries.  Stage
                # the latter in the same disposable bundle shape so the
                # native journey exercises the real AppKit/LaunchServices
                # launch boundary without modifying the product candidate.
                from .local_vm_macos import stage_native_ui_bundle

                native_descriptor = {
                    **descriptor,
                    "ui": str(stage_native_ui_bundle(run_dir, descriptor["ui"])),
                }
            state["runtime"] = runtime
            _write_json(run_dir / "platform.json", state)
            native_environment = _native_ui_environment(args.platform, runtime)
            native_task_timeout = min(
                args.timeout, _NATIVE_UI_TASK_TIMEOUT_CAP_SECONDS,
            )
            try:
                native_result = _run_native_ui(
                    _native_ui_command(
                        run_dir, native_descriptor, runtime, args.platform, native_task_timeout,
                    ),
                    platform=args.platform,
                    run_dir=run_dir,
                    # The journey is a Python module under torturer; Windows runs
                    # this cwd through the existing interactive user task.
                    cwd=run_dir / "source" / "torturer",
                    logs=logs,
                    timeout=native_task_timeout,
                    environment=native_environment,
                )
            except Exception as error:
                if args.platform in {"windows", "macos"}:
                    from .local_vm_windows import WindowsInteractiveDesktopUnavailable
                    from .local_vm_macos import MacOSInteractiveDesktopUnavailable

                    if isinstance(error, (WindowsInteractiveDesktopUnavailable, MacOSInteractiveDesktopUnavailable)):
                        _record_native_ui_unavailable(run_dir, args.platform, error)
                        state["native_ui_exit_code"] = 1
                        state["native_ui_status"] = "unavailable"
                        state["native_ui_reason"] = str(error)
                        state["status"] = "native-ui-unavailable"
                        _write_json(run_dir / "platform.json", state)
                        return 1
                raise
            state["native_ui_exit_code"] = native_result.returncode
            if native_result.returncode != 0:
                state["status"] = "native-ui-failed"
                _write_json(run_dir / "platform.json", state)
                return native_result.returncode
            state["native_ui_status"] = "passed"
            _write_json(run_dir / "platform.json", state)
        state["status"] = "functional-complete"
        _write_json(run_dir / "platform.json", state)
        return 0
    except Exception as error:
        # A platform helper may have persisted ownership immediately before a
        # native side effect.  Reload that record so the failure marker never
        # erases the supervisor's cleanup inputs.
        try:
            persisted = _read_state(run_dir)
        except LocalVMError:
            persisted = None
        if persisted is not None:
            for key in ("candidate", "runtime", "release"):
                if key in persisted:
                    state[key] = persisted[key]
        state["status"] = "failed"
        state["error"] = f"{type(error).__name__}: {error}"
        _write_json(run_dir / "platform.json", state)
        print(state["error"], file=sys.stderr)
        return 1


def _pid_matches(pid: int, binary: str | None) -> bool:
    if pid <= 0 or not binary or os.name == "nt":
        return False
    if not _pid_alive(pid):
        return False
    # A capability-bearing service may be non-dumpable even to its own UID.
    # Use the VM's existing sudo privilege for this specific procfs lookup.
    observed = subprocess.run(
        ["sudo", "-n", "readlink", "-f", f"/proc/{pid}/exe"],
        capture_output=True, text=True, timeout=5, check=False,
    )
    if observed.returncode:
        if not _pid_alive(pid):
            return False
        raise LocalVMError("cannot inspect service executable; noninteractive sudo readlink is required")
    return observed.stdout.strip() == str(Path(binary).resolve())


def _pid_alive(pid: int) -> bool:
    try:
        return (Path("/proc") / str(pid) / "stat").read_text().rsplit(")", 1)[1].split()[0] != "Z"
    except FileNotFoundError:
        return False


def _checked_process_group(pid: int, binary: str | None) -> str | None:
    if not _pid_matches(pid, binary):
        return None
    observed = subprocess.run(
        ["sudo", "-n", "ps", "-o", "pgid=", "-p", str(pid)],
        capture_output=True, text=True, timeout=5, check=False,
    )
    if observed.returncode:
        if not _pid_alive(pid):
            return None
        raise LocalVMError("cannot inspect service process group; noninteractive sudo ps is required")
    process_group = observed.stdout.strip()
    if _PID.fullmatch(process_group) is None:
        raise LocalVMError("service process group is invalid")
    return process_group


def _signal_process_group(process_group: str, signal_name: str) -> bool:
    result = subprocess.run(
        ["sudo", "-n", "kill", signal_name, "--", f"-{process_group}"],
        capture_output=True, text=True, timeout=5, check=False,
    )
    return result.returncode == 0


def _stop_pid(pid: int, binary: str | None, timeout: float) -> bool:
    if not _pid_alive(pid):
        return True
    process_group = _checked_process_group(pid, binary)
    if process_group is None:
        return False
    if not _signal_process_group(process_group, "-TERM") and _pid_alive(pid):
        raise LocalVMError("could not terminate service process group")
    deadline = time.monotonic() + min(timeout, 3)
    while time.monotonic() < deadline:
        if not _pid_matches(pid, binary):
            return True
        time.sleep(0.05)
    if _pid_matches(pid, binary):
        if not _signal_process_group(process_group, "-KILL") and _pid_alive(pid):
            raise LocalVMError("could not kill service process group")
    deadline = time.monotonic() + min(timeout, 5)
    while time.monotonic() < deadline:
        if not _pid_matches(pid, binary):
            return True
        time.sleep(0.05)
    return False


def _linux_runtime_paths(run_dir: Path, socket: object) -> tuple[Path, Path] | None:
    """Return the one disposable Linux socket and its private parent."""
    if socket is None:
        return None
    if not isinstance(socket, str):
        raise LocalVMError("Linux runtime socket path is invalid")
    socket_path = Path(socket)
    runtime_dir = run_dir.resolve() / ".dobbyvpn-run"
    if (
        not socket_path.is_absolute()
        or socket_path.name != "s"
        or socket_path.parent.resolve() != runtime_dir
    ):
        raise LocalVMError("Linux runtime socket is outside the private run directory")
    return socket_path, runtime_dir


def _cleanup_linux_runtime_state(
    run_dir: Path, socket: object, *, logs: Path, timeout: float, errors: list[str]
) -> None:
    try:
        paths = _linux_runtime_paths(run_dir, socket)
    except LocalVMError as error:
        errors.append(f"cleanup-service: {error}")
        return
    if paths is None:
        return
    socket_path, runtime_dir = paths
    if socket_path.exists():
        _cleanup_logged(
            ["sudo", "-n", "unlink", "--", str(socket_path)],
            cwd=run_dir, logs=logs, label="cleanup-service-socket", timeout=timeout,
            errors=errors,
        )
    # rmdir is deliberately non-recursive: an unexpected file must not be
    # hidden, and no path outside this exact candidate runtime directory is
    # ever passed to sudo.
    if runtime_dir.is_dir():
        _cleanup_logged(
            ["sudo", "-n", "rmdir", "--", str(runtime_dir)],
            cwd=run_dir, logs=logs, label="cleanup-service-runtime", timeout=timeout,
            errors=errors,
        )


def _cleanup_logged(
    command: list[str], *, cwd: Path, logs: Path, label: str, timeout: float,
    errors: list[str], tolerate_returncode: tuple[int, ...] = (),
) -> None:
    try:
        result = _run_logged(
            command, cwd=cwd, logs=logs, label=label, timeout=timeout, check=False
        )
    except Exception as error:
        errors.append(f"{label}: {type(error).__name__}: {error}")
        return
    if result.returncode != 0 and result.returncode not in tolerate_returncode:
        errors.append(f"{label}: command exited {result.returncode}")


def _cleanup_launchd(
    command: list[str], *, cwd: Path, logs: Path, timeout: float,
    errors: list[str],
) -> None:
    """Bootout one exact job, treating only an already-absent job as clean."""
    try:
        result = _run_logged(
            command, cwd=cwd, logs=logs, label="cleanup-service",
            timeout=timeout, check=False,
        )
    except Exception as error:
        errors.append(f"cleanup-service: {type(error).__name__}: {error}")
        return
    if result.returncode != 0:
        output = (result.stdout + result.stderr).decode("utf-8", errors="replace")
        absent = "Could not find service" in output or (
            result.returncode == 3 and "No such process" in output
        )
        if not absent:
            errors.append(f"cleanup-service: command exited {result.returncode}")


def _windows_release_package(run_dir: Path, release: dict[str, Any]) -> Path:
    """Return the one exact staged MSI, rejecting mutable path substitutions."""
    package = release.get("package_path")
    if not isinstance(package, str) or not package:
        raise LocalVMError("recorded MSI path is invalid")
    candidate = Path(package)
    if candidate.is_symlink():
        raise LocalVMError("recorded MSI path must not be a symlink")
    try:
        resolved = _inside(candidate, run_dir)
    except LocalVMError as error:
        raise LocalVMError("recorded MSI path is outside the exact Release staging path") from error
    except OSError as error:
        raise LocalVMError("recorded MSI path is unavailable") from error
    expected = (run_dir / "release" / "windows" / "dobbyVPN-windows-amd64.msi").resolve()
    if resolved != expected or not resolved.is_file():
        raise LocalVMError("recorded MSI path is outside the exact Release staging path")
    return resolved


def cleanup(args: argparse.Namespace) -> int:
    run_dir = _run_dir(args.run_dir)
    state = _read_state(run_dir)
    if state is None:
        return 0
    if state.get("platform") != args.platform:
        raise LocalVMError("cleanup platform does not match platform state")
    logs = run_dir / "logs"
    runtime = state.get("runtime") if isinstance(state.get("runtime"), dict) else {}
    release = state.get("release") if isinstance(state.get("release"), dict) else None
    errors: list[str] = []
    if args.platform == "linux":
        helper = Path(__file__).resolve().parents[1] / "helpers/local/linux/routing-probe-firewall"
        _cleanup_logged(["sudo", "-n", str(helper), "remove"], cwd=run_dir, logs=logs, label="cleanup-routing", timeout=args.timeout, errors=errors)
        interface = runtime.get("network_interface")
        if isinstance(interface, str):
            _cleanup_logged(["sudo", "-n", "ip", "link", "set", "dev", interface, "up"], cwd=run_dir, logs=logs, label="cleanup-uplink", timeout=args.timeout, errors=errors)
        pid: int | None = runtime.get("pid") if isinstance(runtime.get("pid"), int) else None
        pid_file = runtime.get("pid_file")
        if isinstance(pid_file, str):
            try:
                value = Path(pid_file).read_text(encoding="ascii").strip()
            except FileNotFoundError:
                value = ""
            except OSError as error:
                errors.append(f"cleanup-service: cannot read service PID file: {error}")
                value = ""
            if value:
                if not _PID.fullmatch(value):
                    errors.append("cleanup-service: service PID file is invalid")
                else:
                    pid = int(value)
        service_stopped = True
        if isinstance(pid, int):
            try:
                if not _stop_pid(pid, runtime.get("binary"), args.timeout):
                    errors.append("cleanup-service: recorded PID no longer names the candidate binary")
                    service_stopped = False
            except Exception as error:
                errors.append(f"cleanup-service: {type(error).__name__}: {error}")
                service_stopped = False
        if service_stopped:
            _cleanup_linux_runtime_state(
                run_dir, runtime.get("socket"), logs=logs, timeout=args.timeout,
                errors=errors,
            )
    elif args.platform == "macos":
        helper = Path(__file__).resolve().parents[1] / "helpers/local/macos/network-transition"
        _cleanup_logged(["sudo", "-n", str(helper), "routing-remove"], cwd=run_dir, logs=logs, label="cleanup-routing", timeout=args.timeout, errors=errors)
        interface = runtime.get("network_interface")
        if isinstance(interface, str):
            _cleanup_logged(["sudo", "-n", "/sbin/ifconfig", interface, "up"], cwd=run_dir, logs=logs, label="cleanup-uplink", timeout=args.timeout, errors=errors)
        if release is not None:
            # The package's fixed uninstaller owns launchd, its plist/socket,
            # receipt, app bundle, and uninstaller path.  Do not reproduce its
            # root-side deletion logic or accept paths from test state.
            _cleanup_logged(
                ["sudo", "-n", "/usr/local/libexec/dobbyvpn-uninstall"],
                cwd=run_dir, logs=logs, label="cleanup-package",
                timeout=args.timeout, errors=errors,
            )
        else:
            label = runtime.get("launchd_label", "system/com.dobby.vpnservice")
            # bootout is required: KeepAlive would immediately restart the
            # service after a mere launchctl kill.
            _cleanup_launchd(["sudo", "-n", "launchctl", "bootout", str(label)], cwd=run_dir, logs=logs, timeout=args.timeout, errors=errors)
            plist = runtime.get("plist", "/Library/LaunchDaemons/com.dobby.vpnservice.plist")
            control_socket = runtime.get("socket", "/var/run/dobbyvpn/control.sock")
            remove_paths = [value for value in (plist, control_socket) if isinstance(value, str) and value.startswith(("/Library/", "/var/run/"))]
            if remove_paths:
                _cleanup_logged(["sudo", "-n", "rm", "-f", *remove_paths], cwd=run_dir, logs=logs, label="cleanup-macos-state", timeout=args.timeout, errors=errors)
    elif args.platform == "windows":
        from .local_vm_windows import cleanup as cleanup_windows
        try:
            cleanup_windows(run_dir, runtime, logs, args.timeout)
        except Exception as error:
            errors.append(f"cleanup-windows: {type(error).__name__}: {error}")
        if release is not None:
            installed = release.get("installed") is True
            try:
                package = _windows_release_package(run_dir, release)
            except Exception as error:
                errors.append(f"cleanup-package: {type(error).__name__}: {error}")
            else:
                _cleanup_logged(
                    [
                        "msiexec.exe", "/x", str(package), "/qn", "/norestart",
                        "/L*v", str(logs / "windows-uninstall.log"),
                    ],
                    cwd=run_dir, logs=logs, label="cleanup-package", timeout=args.timeout,
                    errors=errors, tolerate_returncode=() if installed else (1605,),
                )
    elif args.platform == "android":
        from .local_vm_android import cleanup as cleanup_android
        try:
            cleanup_android(run_dir, runtime, logs, args.timeout)
        except Exception as error:
            errors.append(f"cleanup-android: {type(error).__name__}: {error}")
    elif args.platform == "ios-simulator":
        from .local_vm_ios import cleanup as cleanup_ios
        try:
            cleanup_ios(run_dir, runtime, logs, args.timeout)
        except Exception as error:
            errors.append(f"cleanup-ios: {type(error).__name__}: {error}")
    state["status"] = "cleaned" if not errors else "cleanup-failed"
    if errors:
        state["cleanup_errors"] = errors
    _write_json(run_dir / "platform.json", state)
    if errors:
        print("; ".join(errors), file=sys.stderr)
        return 1
    return 0


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        return run(args) if args.action == "run" else cleanup(args)
    except LocalVMError as error:
        print(str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
