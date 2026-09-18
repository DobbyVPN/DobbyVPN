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
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
from typing import Any

PLATFORMS = ("linux", "windows", "macos", "android", "ios-simulator")
SIMULATOR_MODES = ("mini", "metal")
DESKTOP_PLATFORMS = frozenset(("linux", "windows", "macos"))
_PID = re.compile(r"^[1-9][0-9]*$")
_IDENTITY = re.compile(r"^[A-Za-z0-9._-]+$")


class LocalVMError(RuntimeError):
    """A bounded local candidate operation failed."""


def _positive_timeout(value: str) -> float:
    try:
        result = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("timeout must be a number") from error
    if not 0 < result < float("inf"):
        raise argparse.ArgumentTypeError("timeout must be positive and finite")
    return result


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
        command.add_argument("--scenario", action="append", dest="scenarios")
        if action == "run":
            command.add_argument("--architecture")
            command.add_argument("--simulator-mode", choices=SIMULATOR_MODES)
            command.add_argument("--skip-deps", action="store_true")
            command.add_argument("--network-interface")
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
    path = _inside(run_dir / name, run_dir)
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
            completed = subprocess.run(
                command,
                cwd=str(cwd),
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=stdout_stream,
                stderr=stderr_stream,
                timeout=timeout,
                check=False,
            )
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
    simulator_mode: str,
) -> dict[str, Any]:
    from .local_vm_ios import run
    return run(run_dir, logs, timeout, architecture, simulator_mode)


def _functional_command(run_dir: Path, descriptor: dict[str, Any], platform: str, timeout: float, scenarios: list[str] | None) -> list[str]:
    logs = run_dir / "logs"
    command = [
        sys.executable, "-m", "torturer_checks.functional", "--platform", platform,
        "--profile", str(run_dir / "profile"), "--output", str(logs / "functional.json"),
        "--raw-log-dir", str(logs), "--platform-version", f"local-{platform}",
        "--lane-timeout-seconds", str(timeout),
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


def _native_ui_command(run_dir: Path, descriptor: dict[str, Any], platform: str, timeout: float) -> list[str]:
    """Build the required real-window journey command for desktop guests."""
    if platform not in {"windows", "macos"}:
        raise LocalVMError(f"native GUI qualification is unsupported on {platform}")
    smoke = run_dir / "source" / ".github" / "scripts" / "native_ui_smoke.py"
    if not smoke.is_file():
        raise LocalVMError("native desktop UI qualification script is missing")
    return [
        sys.executable,
        str(smoke),
        "--platform",
        platform,
        "--ui",
        str(_candidate_path(descriptor, "ui")),
        "--profile",
        str(run_dir / "profile"),
        "--timeout",
        str(min(timeout, 300.0)),
    ]


def run(args: argparse.Namespace) -> int:
    run_dir = _run_dir(args.run_dir)
    source = _required_input(run_dir, "source", directory=True)
    if args.platform != "ios-simulator":
        _required_input(run_dir, "profile")
    if args.platform == "ios-simulator" and args.simulator_mode not in SIMULATOR_MODES:
        raise LocalVMError("ios-simulator requires --simulator-mode mini or metal")
    logs = run_dir / "logs"
    (run_dir / "output").mkdir(parents=True, exist_ok=True)
    state: dict[str, Any] = {
        "platform": args.platform,
        "status": "preparing",
    }
    if args.platform == "ios-simulator":
        state["simulator_mode"] = args.simulator_mode
    _write_json(run_dir / "platform.json", state)
    try:
        if args.platform == "ios-simulator":
            runtime = _start_ios(
                run_dir, logs, args.timeout, args.architecture, args.simulator_mode,
            )
            state.update(runtime=runtime, status="functional-complete", functional_exit_code=0)
            _write_json(run_dir / "platform.json", state)
            return 0
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
            runtime = _start_macos(
                run_dir, descriptor, logs, args.timeout,
                state.get("runtime", {}).get("network_interface"),
            )
        elif args.platform == "windows":
            runtime = _start_windows(run_dir, descriptor, logs, args.timeout)
        elif args.platform == "android":
            runtime = _start_android(run_dir, descriptor, logs, args.timeout)
        state["runtime"] = runtime
        state["status"] = "running"
        _write_json(run_dir / "platform.json", state)
        if args.platform in {"windows", "macos"}:
            native_environment = {**os.environ}
            runtime_environment = runtime.get("environment")
            if isinstance(runtime_environment, dict):
                native_environment.update({
                    str(key): str(value)
                    for key, value in runtime_environment.items()
                    if isinstance(key, str) and isinstance(value, str)
                })
            native_result = _run_logged(
                _native_ui_command(run_dir, descriptor, args.platform, args.timeout),
                cwd=run_dir / "source",
                logs=logs,
                label="native-ui",
                timeout=min(args.timeout, 300.0),
                environment=native_environment,
                check=False,
            )
            state["native_ui_exit_code"] = native_result.returncode
            if native_result.returncode != 0:
                state["status"] = "native-ui-failed"
                _write_json(run_dir / "platform.json", state)
                return native_result.returncode
            state["native_ui_status"] = "passed"
            _write_json(run_dir / "platform.json", state)
        command = _functional_command(run_dir, {**descriptor, "runtime": runtime}, args.platform, args.timeout, args.scenarios)
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
        return result.returncode
    except Exception as error:
        # A platform helper may have persisted ownership immediately before a
        # native side effect.  Reload that record so the failure marker never
        # erases the supervisor's cleanup inputs.
        try:
            persisted = _read_state(run_dir)
        except LocalVMError:
            persisted = None
        if persisted is not None:
            for key in ("candidate", "runtime"):
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


def cleanup(args: argparse.Namespace) -> int:
    run_dir = _run_dir(args.run_dir)
    state = _read_state(run_dir)
    if state is None:
        return 0
    if state.get("platform") != args.platform:
        raise LocalVMError("cleanup platform does not match platform state")
    logs = run_dir / "logs"
    runtime = state.get("runtime") if isinstance(state.get("runtime"), dict) else {}
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
        label = runtime.get("launchd_label", "system/com.dobby.vpnservice")
        # bootout is required: KeepAlive would immediately restart the
        # service after a mere launchctl kill.
        _cleanup_launchd(["sudo", "-n", "launchctl", "bootout", str(label)], cwd=run_dir, logs=logs, timeout=args.timeout, errors=errors)
        interface = runtime.get("network_interface")
        if isinstance(interface, str):
            _cleanup_logged(["sudo", "-n", "/sbin/ifconfig", interface, "up"], cwd=run_dir, logs=logs, label="cleanup-uplink", timeout=args.timeout, errors=errors)
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
