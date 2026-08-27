#!/usr/bin/env python3
from __future__ import annotations

import argparse
import contextlib
import ctypes
from dataclasses import dataclass
import hashlib
import os
import platform
import re
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import urllib.request
import zipfile
from pathlib import Path

from public_output import emit_diagnostic as emit_public_diagnostic
from public_output import public_actions
from windows_process_census import WindowsProcessCensusError, windows_process_census


ROOT_DIR = Path(__file__).resolve().parents[2]
GO_MODULE_DIR = ROOT_DIR / "go_module"
KMP_DIR = ROOT_DIR / "kmp_module"
SERVICES_DIR = KMP_DIR / "services"
TOOLS_DIR = ROOT_DIR / ".local-tools" / "desktop-build"

ANDROID_NDK_VERSION = "27.2.12479018"
ANDROID_PACKAGES = (
    "platforms;android-35",
    "platforms;android-36",
    "build-tools;36.0.0",
    "platform-tools",
    f"ndk;{ANDROID_NDK_VERSION}",
)
ANDROID_TOOLS_VERSION = "11076708"
WINTUN_VERSION = "0.14.1"
WINTUN_AMD64_DLL_SHA256 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
LLVM_LIBCXX_VERSION = "21.1.8"
LLVM_LIBCXX_PACKAGES = (
    (
        "libc++1_21.1.8~++20251221032922+2078da43e25a-1~exp1~20251221153059.70_amd64.deb",
        "d9566cd347beb65bf2e0d504fbffc6ca38c29ac2752292cba515891c84ff0bbd",
    ),
    (
        "libc++abi1_21.1.8~++20251221032922+2078da43e25a-1~exp1~20251221153059.70_amd64.deb",
        "829f02714a9daafac0cc37c81c7d02ae5cb5b63707b524b93e36dcd7e7e5549c",
    ),
)
TRUSTTUNNEL_MACOS_VERSION = "1.0.49"
TRUSTTUNNEL_MACOS_ARCHIVE_SHA256 = "f2dab732d17a885dcc4c81831fa4b263db250f5bea8a151416b518e936979c64"


@dataclass(frozen=True)
class BridgeRelease:
    version: str
    asset_name: str
    archive_sha256: str
    member_name: str
    member_sha256: str


BRIDGE_RELEASES = {
    "windows": BridgeRelease(
        version="1.0.1",
        asset_name="dobby_bridge-windows-x86_64.zip",
        archive_sha256="a7e64db0568547d395bc45e33787f22c7303dca6f5c575c84439e73a70124331",
        member_name="dobby_bridge.dll",
        member_sha256="10e2f921aaa949060bed936e3c361b0967b2ad8b7a71dd983d36abd94c903063",
    ),
    "linux": BridgeRelease(
        version="1.0.1",
        asset_name="libdobby_bridge-linux-x86_64.zip",
        archive_sha256="67536090d74212a5635739d297f5a78fbabda1966d161b12a16bfe487a8c68b9",
        member_name="libdobby_bridge.so",
        member_sha256="2fff96d2631df43168196e222bc2205157d23a2449475364c5b135fbf0aaa0ce",
    ),
}

SERVICE_NAMES = {
    "linux": "ubuntu_grpcvpnserver",
    "macos": "macos_grpcvpnserver",
    "windows": "windows_grpcvpnserver.exe",
}
CLI_NAMES = {
    "linux": "dobby-cli",
    "macos": "dobby-cli",
    "windows": "dobby-cli.exe",
}
MACOS_MINIMUM_SYSTEM_VERSION = "11.0"
PROBE_TIMEOUT_SECONDS = 30
PROCESS_CLEANUP_GRACE_SECONDS = 5
PROCESS_TREE_POLL_INTERVAL_SECONDS = 0.01
# Windows hosted runners can take several seconds to enumerate the complete
# WMI process table.  Keep the proof bounded, but do not mistake normal WMI
# startup latency for an unverifiable process tree.
PROCESS_TREE_QUERY_TIMEOUT_SECONDS = 10
PROCESS_TREE_WATCHER_JOIN_TIMEOUT_SECONDS = PROCESS_TREE_QUERY_TIMEOUT_SECONDS + 2
GOOS_BY_PLATFORM = {
    "linux": "linux",
    "macos": "darwin",
    "windows": "windows",
}
CI_ARCH_BY_PLATFORM = {
    "linux": "amd64",
    "macos": "arm64",
    "windows": "amd64",
}


def log(message: str) -> None:
    print(f"[+] {message}", flush=True)


def fail(message: str) -> None:
    raise SystemExit(f"[!] {message}")


def output_text(output: str | bytes | None) -> str:
    if output is None:
        return ""
    if isinstance(output, bytes):
        return output.decode("utf-8", errors="replace")
    return output


def output_bytes(output: str | bytes | None) -> bytes:
    if output is None:
        return b""
    if isinstance(output, bytes):
        return output
    return output.encode("utf-8", errors="surrogatepass")


class ProcessTreeProofError(RuntimeError):
    """Raised when a child tree cannot be proven to have disappeared."""


def _proc_identity(pid: int) -> tuple[str, str] | None:
    try:
        stat_text = Path(f"/proc/{pid}/stat").read_text(encoding="ascii")
    except (FileNotFoundError, OSError):
        return None
    closing_parenthesis = stat_text.rfind(")")
    fields = stat_text[closing_parenthesis + 2 :].split()
    if len(fields) <= 19:
        return None
    return fields[0], fields[19]


def _active_proc_group_members(group_id: int) -> list[int]:
    members: list[int] = []
    for stat_path in Path("/proc").glob("[0-9]*/stat"):
        try:
            stat_text = stat_path.read_text(encoding="ascii")
        except (FileNotFoundError, OSError):
            continue
        closing_parenthesis = stat_text.rfind(")")
        fields = stat_text[closing_parenthesis + 2 :].split()
        if len(fields) <= 2 or fields[0] == "Z":
            continue
        try:
            process_group = int(fields[2])
            pid = int(stat_text[: stat_text.find(" ")])
        except (ValueError, TypeError):
            continue
        if process_group == group_id:
            members.append(pid)
    return members


def _pid_is_alive(pid: int, expected_identity: tuple[str, str] | None = None) -> bool:
    if os.name == "nt":
        try:
            _, active_pids = windows_process_census(
                pid,
                timeout_seconds=PROCESS_TREE_QUERY_TIMEOUT_SECONDS,
            )
        except WindowsProcessCensusError as error:
            raise ProcessTreeProofError(str(error)) from error
        return pid in active_pids
    if Path("/proc").is_dir():
        identity = _proc_identity(pid)
        # The process state (the first field) changes during normal execution;
        # only the kernel start time is the stable PID-reuse identity.
        if identity is None or (
            expected_identity is not None and identity[1] != expected_identity[1]
        ):
            return False
        if identity[0] == "Z":
            return False
        return True
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError as error:
        raise ProcessTreeProofError(f"pid={pid} proof permission denied") from error
    except OSError as error:
        raise ProcessTreeProofError(f"pid={pid} proof query failed") from error
    return True


def _proc_descendants(root_pid: int) -> set[int]:
    children_path = Path(f"/proc/{root_pid}/task/{root_pid}/children")
    try:
        children = children_path.read_text(encoding="ascii").split()
    except FileNotFoundError:
        return set()
    except OSError as error:
        raise ProcessTreeProofError(f"could not read {children_path}: {error}") from error
    descendants: set[int] = set()
    try:
        pending = [int(child) for child in children]
    except ValueError as error:
        raise ProcessTreeProofError(f"invalid child PID data in {children_path}") from error
    while pending:
        pid = pending.pop()
        if pid in descendants:
            continue
        descendants.add(pid)
        child_path = Path(f"/proc/{pid}/task/{pid}/children")
        try:
            try:
                pending.extend(int(child) for child in child_path.read_text(encoding="ascii").split())
            except ValueError as error:
                raise ProcessTreeProofError(f"invalid child PID data in {child_path}") from error
        except FileNotFoundError:
            continue
        except OSError as error:
            raise ProcessTreeProofError(f"could not read {child_path}: {error}") from error
    return descendants


def _ps_descendants(root_pid: int) -> set[int]:
    result = subprocess.run(
        ["ps", "-eo", "pid=,ppid="],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=False,
        timeout=PROCESS_TREE_QUERY_TIMEOUT_SECONDS,
    )
    if result.returncode != 0:
        raise ProcessTreeProofError(
            f"ps process-tree query failed exit={result.returncode} "
            f"stderr={output_text(result.stderr).strip()}"
        )
    if result.stderr:
        raise ProcessTreeProofError(
            f"ps process-tree query emitted stderr={output_text(result.stderr).strip()}"
        )
    children_by_parent: dict[int, set[int]] = {}
    for line in output_text(result.stdout).splitlines():
        fields = line.split()
        if len(fields) != 2:
            continue
        try:
            pid, parent_pid = (int(field) for field in fields)
        except ValueError:
            continue
        children_by_parent.setdefault(parent_pid, set()).add(pid)
    descendants: set[int] = set()
    pending = list(children_by_parent.get(root_pid, set()))
    while pending:
        pid = pending.pop()
        if pid in descendants:
            continue
        descendants.add(pid)
        pending.extend(children_by_parent.get(pid, set()))
    return descendants


def _windows_process_snapshot(root_pid: int) -> tuple[set[int], set[int]]:
    try:
        return windows_process_census(
            root_pid,
            timeout_seconds=PROCESS_TREE_QUERY_TIMEOUT_SECONDS,
        )
    except WindowsProcessCensusError as error:
        raise ProcessTreeProofError(str(error)) from error


def _process_tree_snapshot(root_pid: int) -> tuple[set[int], str, set[int] | None]:
    if os.name == "nt":
        descendants, active_pids = _windows_process_snapshot(root_pid)
        return descendants, "powershell-cim", active_pids
    if Path("/proc").is_dir():
        return _proc_descendants(root_pid), "procfs", None
    return _ps_descendants(root_pid), "ps", None


class ProcessTreeTracker:
    def __init__(self, root_pid: int) -> None:
        self.root_pid = root_pid
        self.observed: set[int] = {root_pid}
        self.identities: dict[int, tuple[str, str]] = {}
        self.active_pids: set[int] | None = None
        self.source = "unknown"
        self.error: ProcessTreeProofError | None = None
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._poll, name=f"dobby-process-tree-{root_pid}", daemon=True)

    def start(self) -> None:
        self._sample()
        self._thread.start()

    def _sample(self) -> None:
        try:
            descendants, source, active_pids = _process_tree_snapshot(self.root_pid)
        except (ProcessTreeProofError, OSError, ValueError, subprocess.SubprocessError) as error:
            with self._lock:
                self.error = error if isinstance(error, ProcessTreeProofError) else ProcessTreeProofError(str(error))
            return
        with self._lock:
            self.observed.update(descendants)
            for pid in self.observed:
                identity = _proc_identity(pid)
                if identity is not None:
                    self.identities.setdefault(pid, identity)
            self.source = source
            self.active_pids = active_pids

    def _poll(self) -> None:
        while not self._stop.wait(PROCESS_TREE_POLL_INTERVAL_SECONDS):
            self._sample()

    def signal_descendants(self, signum: int) -> None:
        with self._lock:
            pids = tuple(self.observed)
        for pid in pids:
            if pid == self.root_pid:
                continue
            expected_identity = self.identities.get(pid)
            if expected_identity is not None:
                current_identity = _proc_identity(pid)
                if current_identity is None or current_identity[1] != expected_identity[1]:
                    continue
            try:
                os.kill(pid, signum)
            except ProcessLookupError:
                continue
            except OSError as error:
                with self._lock:
                    self.error = ProcessTreeProofError(
                        f"could not signal descendant pid={pid} value={signum}: {error}"
                    )

    def observed_pids(self) -> tuple[int, ...]:
        with self._lock:
            return tuple(sorted(self.observed))

    def prove_gone(self, group_id: int) -> str:
        self._stop.set()
        self._thread.join(timeout=PROCESS_TREE_WATCHER_JOIN_TIMEOUT_SECONDS)
        if self._thread.is_alive():
            raise ProcessTreeProofError("process-tree watcher did not stop")
        self._sample()
        with self._lock:
            error = self.error
            observed = tuple(sorted(self.observed))
            source = self.source
            active_pids = self.active_pids
        if error is not None:
            raise error
        if os.name == "nt":
            if active_pids is None:
                raise ProcessTreeProofError("Windows process census is unavailable")
            survivors = [
                pid for pid in observed if pid != self.root_pid and pid in active_pids
            ]
        else:
            survivors = [
                pid
                for pid in observed
                if pid != self.root_pid and _pid_is_alive(pid, self.identities.get(pid))
            ]
        if survivors:
            raise ProcessTreeProofError(f"descendant survivors={survivors}")
        if os.name != "nt":
            if Path("/proc").is_dir():
                group_survivors = _active_proc_group_members(group_id)
                if group_survivors:
                    raise ProcessTreeProofError(f"process-group survivors={group_survivors}")
            else:
                try:
                    os.killpg(group_id, 0)
                except ProcessLookupError:
                    pass
                except PermissionError as error:
                    raise ProcessTreeProofError("process-group proof permission denied") from error
                else:
                    raise ProcessTreeProofError(f"process-group survivor group={group_id}")
        return f"tree=gone source={source} observed_pids={len(observed)}"


def attach_process_tree_tracker(process: subprocess.Popen[str]) -> ProcessTreeTracker:
    tracker = ProcessTreeTracker(process.pid)
    tracker.start()
    process._dobby_process_tree_tracker = tracker  # type: ignore[attr-defined]
    return tracker


def process_tree_tracker(process: subprocess.Popen[str]) -> ProcessTreeTracker:
    tracker = getattr(process, "_dobby_process_tree_tracker", None)
    if isinstance(tracker, ProcessTreeTracker):
        return tracker
    return attach_process_tree_tracker(process)


def emit_process_diagnostic(prefix: str, output: str | bytes | None = None) -> None:
    """Emit a failed child-process diagnostic without discarding its output."""
    if public_actions():
        emit_public_diagnostic("desktop-build", (prefix, output), root_dir=ROOT_DIR)
        return
    print(prefix, file=sys.stderr, flush=True)
    text = output_text(output)
    if text:
        sys.stderr.write(text)
        if not text.endswith("\n"):
            sys.stderr.write("\n")
        sys.stderr.flush()


def process_group_options() -> dict[str, int | bool]:
    if os.name == "nt":
        return {"creationflags": getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)}
    return {"start_new_session": True}


def retain_process_diagnostics(
    kind: str,
    stdout: str | bytes | None,
    stderr: str | bytes | None,
    status: str,
) -> Path:
    """Retain complete child streams in a unique owner-only file."""
    handle = open_service_log(kind, binary=True)
    try:
        handle.write(f"status={status}\n".encode("utf-8"))
        for label, output in (("stdout", stdout), ("stderr", stderr)):
            handle.write(f"--- {label} ---\n".encode("utf-8"))
            data = output_bytes(output)
            handle.write(data)
            if data and not data.endswith(b"\n"):
                handle.write(b"\n")
        handle.flush()
        os.fsync(handle.fileno())
    finally:
        handle.close()
    return Path(handle.name)


def terminate_process_group(
    process: subprocess.Popen[str],
    grace_seconds: float = PROCESS_CLEANUP_GRACE_SECONDS,
) -> str:
    """Terminate a child process and all descendants, escalating if needed."""
    group_id = getattr(process, "_dobby_process_group_id", process.pid)
    tracker = process_tree_tracker(process)
    if os.name == "nt":
        try:
            process.send_signal(signal.CTRL_BREAK_EVENT)
        except ProcessLookupError:
            pass
        except (AttributeError, OSError, ValueError) as error:
            raise ProcessTreeProofError(f"could not signal process: {error}") from error
    else:
        try:
            os.killpg(group_id, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessTreeProofError(
                f"could not terminate process group={group_id}: {error}"
            ) from error
        tracker.signal_descendants(signal.SIGTERM)
    try:
        process.wait(timeout=grace_seconds)
    except subprocess.TimeoutExpired:
        pass

    try:
        return tracker.prove_gone(group_id)
    except ProcessTreeProofError as first_error:
        proof_error = first_error

    if os.name == "nt":
        try:
            result = subprocess.run(
                ["taskkill", "/T", "/F", "/PID", str(process.pid)],
                check=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=False,
                timeout=grace_seconds,
            )
            retain_process_diagnostics(
                "process-cleanup",
                result.stdout,
                result.stderr,
                f"taskkill-exit-{result.returncode}",
            )
            for pid in tracker.observed_pids():
                if pid == process.pid:
                    continue
                descendant_cleanup = subprocess.run(
                    ["taskkill", "/T", "/F", "/PID", str(pid)],
                    check=False,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=False,
                    timeout=grace_seconds,
                )
                retain_process_diagnostics(
                    "process-cleanup-descendant",
                    descendant_cleanup.stdout,
                    descendant_cleanup.stderr,
                    f"taskkill-pid-{pid}-exit-{descendant_cleanup.returncode}",
                )
        except (OSError, subprocess.SubprocessError) as error:
            emit_process_diagnostic("[!] Windows descendant cleanup failed:", str(error))
        try:
            process.kill()
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessTreeProofError(f"could not kill process: {error}") from error
    else:
        tracker.signal_descendants(signal.SIGKILL)
        try:
            os.killpg(group_id, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessTreeProofError(
                f"could not kill process group={group_id}: {error}"
            ) from error
    try:
        process.wait(timeout=grace_seconds)
    except subprocess.TimeoutExpired:
        pass
    final_deadline = time.monotonic() + grace_seconds
    final_error: ProcessTreeProofError | None = None
    while True:
        try:
            return tracker.prove_gone(group_id)
        except ProcessTreeProofError as error:
            final_error = error
            remaining = final_deadline - time.monotonic()
            if remaining <= 0:
                raise ProcessTreeProofError(
                    f"process tree cleanup failed first={proof_error} final={final_error}"
                ) from final_error
            time.sleep(min(PROCESS_TREE_POLL_INTERVAL_SECONDS, remaining))


def run_bounded_capture(
    command: list[str],
    cwd: Path = ROOT_DIR,
    timeout_seconds: int = PROBE_TIMEOUT_SECONDS,
) -> subprocess.CompletedProcess[str]:
    process = subprocess.Popen(
        command,
        cwd=str(cwd),
        text=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        **process_group_options(),
    )
    process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
    attach_process_tree_tracker(process)
    try:
        stdout, stderr = process.communicate(timeout=timeout_seconds)
    except subprocess.TimeoutExpired as error:
        partial_stdout = getattr(error, "stdout", None) or getattr(error, "output", None)
        partial_stderr = getattr(error, "stderr", None)
        captured_stdout = partial_stdout or b""
        captured_stderr = partial_stderr or b""
        try:
            tree_proof = terminate_process_group(process)
        except ProcessTreeProofError as error:
            tree_proof = f"tree-proof-failed={error}"
            retain_process_diagnostics("probe", captured_stdout, captured_stderr, f"timeout-{timeout_seconds}s;{tree_proof}")
            raise
        try:
            stdout, stderr = process.communicate(timeout=PROCESS_CLEANUP_GRACE_SECONDS)
        except subprocess.TimeoutExpired as cleanup_error:
            stdout = getattr(cleanup_error, "stdout", None) or partial_stdout or ""
            stderr = getattr(cleanup_error, "stderr", None) or partial_stderr or ""
        retain_process_diagnostics("probe", stdout, stderr, f"timeout-{timeout_seconds}s;{tree_proof}")
        stdout_text = output_text(stdout)
        stderr_text = output_text(stderr)
        timeout_error = subprocess.TimeoutExpired(
            command,
            timeout_seconds,
            output=stdout_text,
            stderr=stderr_text,
        )
        raise timeout_error from error
    try:
        tree_proof = process_tree_tracker(process).prove_gone(process._dobby_process_group_id)  # type: ignore[attr-defined]
    except ProcessTreeProofError as error:
        try:
            tree_proof = terminate_process_group(process)
        except ProcessTreeProofError as cleanup_error:
            retain_process_diagnostics(
                "probe",
                stdout,
                stderr,
                f"exit-{process.returncode};tree-proof-failed={error};cleanup-failed={cleanup_error}",
            )
            raise
    retain_process_diagnostics("probe", stdout, stderr, f"exit-{process.returncode};{tree_proof}")
    return subprocess.CompletedProcess(
        command,
        process.returncode,
        output_text(stdout),
        output_text(stderr),
    )


def sha256_file(path: Path) -> str:
    with path.open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


def run(
    command: list[str],
    cwd: Path = ROOT_DIR,
    env: dict[str, str] | None = None,
    input_text: str | None = None,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    printable = " ".join(command)
    log(f"$ {printable}")
    try:
        result = subprocess.run(
            command,
            cwd=str(cwd),
            env=env or os.environ.copy(),
            input=input_text,
            text=True,
        )
    except FileNotFoundError as error:
        fail(f"Command was not found: {error.filename}")
    if check and result.returncode != 0:
        fail(f"Command failed with exit code {result.returncode}: {printable}")
    return result


def run_capture(command: list[str], cwd: Path = ROOT_DIR) -> str | None:
    printable = " ".join(command)
    try:
        result = run_bounded_capture(command, cwd)
    except FileNotFoundError as error:
        emit_process_diagnostic(f"[!] Probe command was not found: {printable}: {error}")
        return None
    except subprocess.TimeoutExpired as error:
        emit_process_diagnostic(
            f"[!] Probe timed out after {error.timeout}s: {printable}",
            error.stdout or error.output,
        )
        emit_process_diagnostic("[!] Probe stderr:", error.stderr)
        return None
    if result.returncode != 0:
        emit_process_diagnostic(
            f"[!] Probe failed with exit code {result.returncode}: {printable}",
            result.stdout,
        )
        emit_process_diagnostic("[!] Probe stderr:", result.stderr)
        return None
    if result.stderr:
        emit_process_diagnostic(f"[!] Probe diagnostics: {printable}", result.stderr)
    # Some version probes (notably ``java -version``) write their successful
    # version banner to stderr rather than stdout.  Keep the complete stderr
    # visible above, but use it as the probe value when stdout is empty so a
    # valid tool is not misclassified as unavailable.
    return (result.stdout or result.stderr or "").strip()


def set_env(name: str, value: str) -> None:
    os.environ[name] = value
    github_env = os.environ.get("GITHUB_ENV")
    if github_env:
        with open(github_env, "a", encoding="utf-8") as handle:
            handle.write(f"{name}={value}\n")


def prepend_path(path: Path) -> None:
    path_str = str(path)
    if not path.exists():
        return
    current = os.environ.get("PATH", "")
    parts = current.split(os.pathsep) if current else []
    if path_str not in parts:
        os.environ["PATH"] = path_str + os.pathsep + current
    github_path = os.environ.get("GITHUB_PATH")
    if github_path:
        with open(github_path, "a", encoding="utf-8") as handle:
            handle.write(path_str + "\n")


def download(url: str, output: Path) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    log(f"Downloading {url}")
    if shutil.which("curl"):
        run(
            [
                "curl",
                "--fail",
                "--location",
                "--show-error",
                "--http1.1",
                "--retry",
                "5",
                "--retry-delay",
                "2",
                "--connect-timeout",
                "60",
                "--max-time",
                "900",
                "--retry-max-time",
                "1200",
                "--continue-at",
                "-",
                url,
                "-o",
                str(output),
            ]
        )
        return

    request = urllib.request.Request(url, headers={"User-Agent": "DobbyVPN desktop_build.py"})
    with urllib.request.urlopen(request, timeout=120) as response:
        with open(output, "wb") as handle:
            shutil.copyfileobj(response, handle)


def host_platform() -> str:
    system = platform.system().lower()
    if system == "linux":
        return "linux"
    if system == "darwin":
        return "macos"
    if system == "windows":
        return "windows"
    fail(f"Unsupported host platform: {platform.system()}")


def normalize_platform(value: str) -> str:
    aliases = {
        "current": "current",
        "all": "all",
        "ubuntu": "linux",
        "linux": "linux",
        "darwin": "macos",
        "mac": "macos",
        "macos": "macos",
        "windows": "windows",
        "win": "windows",
    }
    normalized = aliases.get(value.lower())
    if not normalized:
        fail(f"Unsupported platform: {value}")
    return normalized


def selected_platforms(value: str) -> list[str]:
    normalized = normalize_platform(value)
    if normalized == "current":
        return [host_platform()]
    if normalized == "all":
        return ["linux", "macos", "windows"]
    return [normalized]


def go_arch_from_machine() -> str:
    machine = platform.machine().lower()
    if machine in ("x86_64", "amd64"):
        return "amd64"
    if machine in ("aarch64", "arm64"):
        return "arm64"
    fail(f"Unsupported CPU architecture: {platform.machine()}")


def adoptium_arch() -> str:
    arch = go_arch_from_machine()
    if arch == "amd64":
        return "x64"
    if arch == "arm64":
        return "aarch64"
    fail(f"Unsupported CPU architecture: {platform.machine()}")


def go_version() -> str:
    return (ROOT_DIR / ".go-version").read_text(encoding="utf-8").strip()


def command_exists(name: str) -> bool:
    return shutil.which(name) is not None


def bootstrap_local_tools() -> None:
    version = go_version()
    prepend_path(TOOLS_DIR / f"go-{version}" / "bin")
    prepend_path(TOOLS_DIR / "jdk-17" / "bin")
    prepend_path(TOOLS_DIR / "android-sdk" / "cmdline-tools" / "latest" / "bin")
    prepend_path(TOOLS_DIR / "android-sdk" / "platform-tools")


def local_go_root() -> Path:
    return TOOLS_DIR / f"go-{go_version()}"


def go_root_is_complete(root: Path) -> bool:
    """Return whether a local Go tree contains its executable and stdlib."""
    executable = root / "bin" / ("go.exe" if host_platform() == "windows" else "go")
    return executable.is_file() and (root / "src" / "runtime").is_dir()


def configure_go_root(go_executable: Path) -> None:
    """Bind Go's runtime root to the installation that owns the executable.

    Go distributions extracted below ``.local-tools`` are relocatable, but
    the ``go`` launcher cannot infer ``GOROOT`` when its installation is not
    under a standard system prefix.  This is especially visible on Windows:
    the initial version probe may succeed only after the root is explicit,
    and later module commands can otherwise fail inside the Go runtime.
    """
    try:
        root = go_executable.resolve().parent.parent
    except OSError:
        return
    if (root / "bin").is_dir():
        set_env("GOROOT", str(root))


def configure_go_module_proxy() -> None:
    """Keep Go's normal module proxy when a stale blank setting is present.

    Go also reads the per-user ``go env`` file.  A previously persisted
    ``GOPROXY=`` therefore survives the runner's environment sanitization and
    makes ``go mod download`` fail with an empty proxy list.  Preserve every
    explicit non-empty policy (including ``off``), while making the default
    behavior explicit and equivalent to an untouched Go installation.
    """
    if not os.environ.get("GOPROXY", "").strip():
        set_env("GOPROXY", "https://proxy.golang.org,direct")


def find_go() -> Path | None:
    version = go_version()
    candidates: list[Path] = []
    if shutil.which("go"):
        candidates.append(Path(shutil.which("go") or ""))
    candidates.append(local_go_root() / "bin" / ("go.exe" if host_platform() == "windows" else "go"))

    for candidate in candidates:
        if candidate.exists():
            try:
                candidate_root = candidate.resolve().parent.parent
            except OSError:
                continue
            if not go_root_is_complete(candidate_root):
                continue
            configure_go_root(candidate)
            output = run_capture([str(candidate), "version"])
            if output and f"go{version}" in output:
                return candidate.parent
    return None


def install_go(skip_deps: bool) -> None:
    found = find_go()
    if found:
        prepend_path(found)
        log(f"Go {go_version()} already available")
        return
    if skip_deps:
        fail(f"Go {go_version()} is required")

    current = host_platform()
    goos = {"linux": "linux", "macos": "darwin", "windows": "windows"}[current]
    arch = go_arch_from_machine()
    suffix = "zip" if current == "windows" else "tar.gz"
    archive = TOOLS_DIR / "downloads" / f"go{go_version()}.{goos}-{arch}.{suffix}"
    extract_dir = Path(tempfile.mkdtemp(prefix="dobby-go-"))
    go_root = local_go_root()

    download(f"https://go.dev/dl/go{go_version()}.{goos}-{arch}.{suffix}", archive)
    shutil.rmtree(go_root, ignore_errors=True)
    try:
        if suffix == "zip":
            with zipfile.ZipFile(archive) as zip_file:
                zip_file.extractall(extract_dir)
        else:
            with tarfile.open(archive) as tar_file:
                tar_file.extractall(extract_dir)
        extracted_root = extract_dir / "go"
        if not go_root_is_complete(extracted_root):
            fail(f"Go archive does not contain a complete standard-library tree: {archive}")
        if go_root.exists():
            try:
                shutil.rmtree(go_root)
            except OSError as error:
                fail(f"cannot replace incomplete Go installation {go_root}: {error}")
        if go_root.exists():
            fail(f"cannot replace incomplete Go installation {go_root}: path remains after removal")
        go_root.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(extracted_root), go_root)
    finally:
        shutil.rmtree(extract_dir, ignore_errors=True)

    configure_go_root(go_root / "bin" / ("go.exe" if current == "windows" else "go"))
    prepend_path(go_root / "bin")
    log(f"Installed Go {go_version()} into {go_root}")


def java_executable_name() -> str:
    return "java.exe" if host_platform() == "windows" else "java"


def java_home_from_executable(java_path: Path) -> Path:
    return java_path.resolve().parent.parent


def is_java_17(java_path: Path) -> bool:
    output = run_capture([str(java_path), "-version"])
    return bool(output and 'version "17' in output)


def find_java_17() -> Path | None:
    java_name = java_executable_name()
    java_home = os.environ.get("JAVA_HOME")
    if java_home:
        java = Path(java_home) / "bin" / java_name
        if java.exists() and is_java_17(java):
            return Path(java_home)

    if host_platform() == "macos":
        output = run_capture(["/usr/libexec/java_home", "-v", "17"])
        if output:
            candidate_home = Path(output)
            candidate_java = candidate_home / "bin" / java_name
            if candidate_java.exists() and is_java_17(candidate_java):
                return candidate_home

    java = shutil.which("java")
    if java and is_java_17(Path(java)):
        return java_home_from_executable(Path(java))

    local_java = TOOLS_DIR / "jdk-17" / "bin" / java_name
    if local_java.exists() and is_java_17(local_java):
        return TOOLS_DIR / "jdk-17"

    return None


def install_jdk(skip_deps: bool) -> None:
    found = find_java_17()
    if found:
        set_env("JAVA_HOME", str(found))
        prepend_path(found / "bin")
        log("JDK 17 already available")
        return
    if skip_deps:
        fail("JDK 17 is required")

    current = host_platform()
    adoptium_os = {"linux": "linux", "macos": "mac", "windows": "windows"}[current]
    suffix = "zip" if current == "windows" else "tar.gz"
    archive = TOOLS_DIR / "downloads" / f"temurin-17-{adoptium_os}-{adoptium_arch()}.{suffix}"
    extract_dir = Path(tempfile.mkdtemp(prefix="dobby-jdk-"))
    jdk_root = TOOLS_DIR / "jdk-17"
    url = (
        "https://api.adoptium.net/v3/binary/latest/17/ga/"
        f"{adoptium_os}/{adoptium_arch()}/jdk/hotspot/normal/eclipse"
    )

    download(url, archive)
    shutil.rmtree(jdk_root, ignore_errors=True)
    try:
        if suffix == "zip":
            with zipfile.ZipFile(archive) as zip_file:
                zip_file.extractall(extract_dir)
        else:
            with tarfile.open(archive) as tar_file:
                tar_file.extractall(extract_dir)

        java_name = java_executable_name()
        java_files = list(extract_dir.rglob(f"bin/{java_name}"))
        if not java_files:
            fail("Downloaded JDK archive does not contain java")
        shutil.move(str(java_home_from_executable(java_files[0])), jdk_root)
    finally:
        shutil.rmtree(extract_dir, ignore_errors=True)

    set_env("JAVA_HOME", str(jdk_root))
    prepend_path(jdk_root / "bin")
    log(f"Installed JDK 17 into {jdk_root}")


def sdkmanager_name() -> str:
    return "sdkmanager.bat" if host_platform() == "windows" else "sdkmanager"


def infer_android_home_from_sdkmanager(sdkmanager: Path) -> Path | None:
    try:
        return sdkmanager.resolve().parents[3]
    except IndexError:
        return None


def find_sdkmanager() -> tuple[Path, Path] | None:
    sdkmanager = shutil.which(sdkmanager_name())
    if sdkmanager:
        android_home = infer_android_home_from_sdkmanager(Path(sdkmanager))
        if android_home:
            return Path(sdkmanager), android_home

    for env_name in ("ANDROID_HOME", "ANDROID_SDK_ROOT"):
        sdk_root = os.environ.get(env_name)
        if not sdk_root:
            continue
        manager = Path(sdk_root) / "cmdline-tools" / "latest" / "bin" / sdkmanager_name()
        if manager.exists():
            return manager, Path(sdk_root)

    sdk_root = TOOLS_DIR / "android-sdk"
    manager = sdk_root / "cmdline-tools" / "latest" / "bin" / sdkmanager_name()
    if manager.exists():
        return manager, sdk_root
    return None


def android_packages_installed(sdk_root: Path) -> bool:
    return (
        (sdk_root / "platforms" / "android-35").is_dir()
        and (sdk_root / "platforms" / "android-36").is_dir()
        and (sdk_root / "build-tools" / "36.0.0").is_dir()
        and (sdk_root / "ndk" / ANDROID_NDK_VERSION).is_dir()
    )


def configure_android_env(sdk_root: Path) -> None:
    set_env("ANDROID_HOME", str(sdk_root))
    set_env("ANDROID_SDK_ROOT", str(sdk_root))
    prepend_path(sdk_root / "cmdline-tools" / "latest" / "bin")
    prepend_path(sdk_root / "platform-tools")


def ensure_android_tools_executable(sdk_root: Path) -> None:
    if host_platform() == "windows":
        return
    tools_bin = sdk_root / "cmdline-tools" / "latest" / "bin"
    if not tools_bin.is_dir():
        return
    for tool in tools_bin.iterdir():
        if tool.is_file():
            try:
                tool.chmod(tool.stat().st_mode | 0o111)
            except PermissionError:
                if TOOLS_DIR in sdk_root.resolve().parents:
                    fail(f"Android SDK tool is not writable: {tool}")
                log(f"Android SDK tools are not writable, leaving permissions unchanged: {tools_bin}")
                return


def install_android_sdk(skip_deps: bool) -> None:
    found = find_sdkmanager()
    if found:
        sdkmanager, sdk_root = found
        configure_android_env(sdk_root)
        ensure_android_tools_executable(sdk_root)
        if android_packages_installed(sdk_root):
            log("Android SDK already available")
            return
        if skip_deps:
            fail("Android SDK packages are required")
    elif skip_deps:
        fail("Android SDK command line tools are required")
    else:
        sdk_root = Path(
            os.environ.get("ANDROID_HOME")
            or os.environ.get("ANDROID_SDK_ROOT")
            or TOOLS_DIR / "android-sdk"
        )
        configure_android_env(sdk_root)
        sdkmanager = sdk_root / "cmdline-tools" / "latest" / "bin" / sdkmanager_name()

        current = host_platform()
        tools_os = {"linux": "linux", "macos": "mac", "windows": "win"}[current]
        tools_zip = TOOLS_DIR / "downloads" / f"android-commandlinetools-{tools_os}.zip"
        tools_dir = sdk_root / "cmdline-tools"

        download(
            "https://dl.google.com/android/repository/"
            f"commandlinetools-{tools_os}-{ANDROID_TOOLS_VERSION}_latest.zip",
            tools_zip,
        )
        shutil.rmtree(tools_dir / "latest", ignore_errors=True)
        shutil.rmtree(tools_dir / "cmdline-tools", ignore_errors=True)
        tools_dir.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(tools_zip) as zip_file:
            zip_file.extractall(tools_dir)
        shutil.move(str(tools_dir / "cmdline-tools"), str(tools_dir / "latest"))

    if not sdkmanager.exists():
        fail(f"sdkmanager was not found at {sdkmanager}")

    ensure_android_tools_executable(sdk_root)
    configure_android_env(sdk_root)
    run([str(sdkmanager), "--licenses"], input_text="y\n" * 100, check=False)
    run([str(sdkmanager), *ANDROID_PACKAGES])
    log("Android SDK packages are installed")


def install_linux_packages(skip_deps: bool) -> None:
    if host_platform() != "linux":
        return
    required_commands = ["curl", "unzip", "zip", "git", "gcc", "g++"]
    missing_commands = [name for name in required_commands if not command_exists(name)]
    if not missing_commands:
        return
    if skip_deps:
        fail(f"Missing required commands: {', '.join(missing_commands)}")
    if not command_exists("apt-get"):
        fail(f"Install required commands manually: {', '.join(missing_commands)}")

    sudo = [] if os.geteuid() == 0 else ["sudo"]
    packages = [
        "ca-certificates",
        "curl",
        "unzip",
        "zip",
        "git",
        "build-essential",
        "gcc",
        "g++",
        "pkg-config",
        "iproute2",
    ]
    run([*sudo, "apt-get", "update"])
    run([*sudo, "apt-get", "install", "-y", *packages])


def ensure_compiler(target_platform: str, skip_deps: bool) -> None:
    if target_platform == "linux":
        install_linux_packages(skip_deps)
        if not command_exists("gcc") or not command_exists("g++"):
            fail("gcc and g++ are required for the Linux gRPC VPN service")
    elif target_platform == "macos":
        if run_capture(["xcode-select", "-p"]):
            return
        if not skip_deps:
            run(["xcode-select", "--install"], check=False)
        fail("Install Xcode Command Line Tools, then run the script again")
    elif target_platform == "windows":
        mingw_bin = Path("C:/ProgramData/chocolatey/lib/mingw/tools/install/mingw64/bin")
        # Common standalone WinLibs installations use C:/mingw64. Prefer that
        # no-space path over WinGet's Program Files shim when it is available.
        prepend_path(mingw_bin)
        prepend_path(Path("C:/mingw64/bin"))
        usable, diagnostic = probe_windows_gcc()
        if usable:
            return
        if skip_deps:
            fail(
                "A working x86_64 MinGW gcc is required for the Windows gRPC VPN service "
                f"({diagnostic})"
            )
        if not command_exists("choco"):
            fail(f"Install or repair MinGW gcc, or install Chocolatey ({diagnostic})")
        run(["choco", "install", "mingw", "-y"])
        prepend_path(mingw_bin)
        usable, diagnostic = probe_windows_gcc()
        if not usable:
            fail(f"MinGW gcc remained unusable after installation ({diagnostic})")


def probe_windows_gcc() -> tuple[bool, str]:
    candidate = shutil.which("gcc")
    if candidate is None:
        return False, "compiler=missing"
    try:
        executable = Path(candidate).resolve(strict=True)
    except OSError:
        return False, "compiler=unresolvable"
    if any(character.isspace() for character in str(executable.parent)):
        # Go/cgo passes GCC's derived linker paths through its external-link
        # command. MinGW distributions rooted below "Program Files" split
        # those paths at the space and fail during the final link.
        return False, "compiler=unsupported_path_contains_whitespace"
    # WinGet can expose a gcc symlink without placing the real executable's
    # adjacent runtime DLLs on PATH. Prefer the resolved bin directory both for
    # this probe and for the later Go/cgo process.
    prepend_path(executable.parent)
    command = [str(executable), "-dumpmachine"]
    try:
        result = run_bounded_capture(command)
    except OSError as error:
        emit_process_diagnostic(f"[!] Compiler probe could not start: {command}: {error}")
        return False, f"compiler=launch_failed errno={error.errno or 0}"
    except subprocess.TimeoutExpired as error:
        emit_process_diagnostic(
            f"[!] Compiler probe timed out after {error.timeout}s: {' '.join(command)}",
            error.stdout or error.output,
        )
        emit_process_diagnostic("[!] Compiler probe stderr:", error.stderr)
        return False, f"compiler=timeout timeout_seconds={error.timeout}"
    if result.returncode != 0:
        emit_process_diagnostic(
            f"[!] Compiler probe failed with exit code {result.returncode}: {' '.join(command)}",
            result.stdout,
        )
        emit_process_diagnostic("[!] Compiler probe stderr:", result.stderr)
    target = result.stdout.strip()
    if result.returncode == 0 and target == "x86_64-w64-mingw32":
        return True, f"compiler=ready target={target}"
    safe_target = target if re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", target) else "invalid"
    return False, f"compiler=unusable exit_code={result.returncode} target={safe_target}"


def install_wintun(skip_deps: bool) -> None:
    if host_platform() != "windows":
        return
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    arch = go_arch_from_machine()
    artifact = GO_MODULE_DIR / "wintun.dll"
    staged = SERVICES_DIR / "wintun.dll"
    source = artifact if artifact.is_file() else staged if staged.is_file() else None
    if source is None:
        if skip_deps:
            fail("wintun.dll is required for Windows CLI checks")
        source = artifact
        archive = TOOLS_DIR / "downloads" / f"wintun-{WINTUN_VERSION}.zip"
        member_name = f"wintun/bin/{arch}/wintun.dll"
        temporary = artifact.with_suffix(".dll.tmp")
        download(f"https://www.wintun.net/builds/wintun-{WINTUN_VERSION}.zip", archive)
        try:
            with zipfile.ZipFile(archive) as zip_file:
                member = zip_file.getinfo(member_name)
                if member.is_dir() or Path(member.filename).name != "wintun.dll":
                    fail("Wintun archive member is invalid")
                with zip_file.open(member) as input_file, open(temporary, "wb") as output_file:
                    shutil.copyfileobj(input_file, output_file)
            temporary.replace(artifact)
        finally:
            temporary.unlink(missing_ok=True)

    if arch == "amd64" and sha256_file(source) != WINTUN_AMD64_DLL_SHA256:
        fail("Wintun amd64 DLL checksum mismatch")
    if source != artifact:
        shutil.copyfile(source, artifact)
    if source != staged:
        shutil.copyfile(source, staged)
    log(f"Staged checksum-pinned Wintun DLL: {staged.name}")


def install_windows_bridge(skip_deps: bool) -> None:
    """Install and stage the checksum-pinned Windows native bridge."""
    if host_platform() != "windows":
        return
    bridge_dir = GO_MODULE_DIR / "lib" / "windows"
    bridge_dir.mkdir(parents=True, exist_ok=True)
    release = BRIDGE_RELEASES["windows"]
    bridge = bridge_dir / release.member_name
    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        if skip_deps:
            fail("dobby_bridge.dll is required for the Windows gRPC VPN service")
        archive = TOOLS_DIR / "downloads" / f"{Path(release.asset_name).stem}-v{release.version}.zip"
        download(
            "https://github.com/DobbyVPN/go-go-tunnel/releases/download/"
            f"v{release.version}/{release.asset_name}",
            archive,
        )
        if sha256_file(archive) != release.archive_sha256:
            fail("Windows native bridge archive checksum mismatch")
        with zipfile.ZipFile(archive) as zip_file:
            for member in zip_file.infolist():
                member_path = Path(member.filename)
                if member.is_dir() or member_path.is_absolute() or ".." in member_path.parts:
                    continue
                if member_path.name not in {
                    "dobby_bridge.dll",
                    "dobby_bridge.lib",
                    "dobby_bridge.a",
                    "libdobby_bridge.a",
                }:
                    continue
                with zip_file.open(member) as source, open(bridge_dir / member_path.name, "wb") as target:
                    shutil.copyfileobj(source, target)
    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        fail("Windows native bridge archive did not contain the expected dobby_bridge.dll")
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    for directory in (GO_MODULE_DIR, SERVICES_DIR):
        shutil.copyfile(bridge, directory / bridge.name)
    log(f"Staged checksum-pinned Windows native bridge: {bridge.name}")


def append_cgo_ldflags(env: dict[str, str], *flags: str) -> None:
    """Append required native linker flags without discarding caller flags."""
    existing = env.get("CGO_LDFLAGS", "").strip()
    env["CGO_LDFLAGS"] = " ".join(part for part in (existing, *flags) if part)


def install_linux_trusttunnel_bridge(skip_deps: bool) -> None:
    """Stage the checksum-pinned Linux bridge for linking and packaging."""
    if host_platform() != "linux":
        return

    release = BRIDGE_RELEASES["linux"]
    bridge = GO_MODULE_DIR / release.member_name
    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        if skip_deps:
            fail("libdobby_bridge.so is required for the Linux gRPC VPN service")

        archive = (
            TOOLS_DIR
            / "downloads"
            / f"{Path(release.asset_name).stem}-v{release.version}.zip"
        )
        if not archive.exists():
            download(
                "https://github.com/DobbyVPN/go-go-tunnel/releases/download/"
                f"v{release.version}/{release.asset_name}",
                archive,
            )
        if sha256_file(archive) != release.archive_sha256:
            fail("TrustTunnel Linux bridge archive checksum mismatch")

        with zipfile.ZipFile(archive) as zip_file:
            candidates = [
                member
                for member in zip_file.infolist()
                if not member.is_dir() and Path(member.filename).name == bridge.name
            ]
            if len(candidates) != 1:
                fail("TrustTunnel Linux bridge archive did not contain exactly one shared library")
            member = candidates[0]
            source = zip_file.open(member)
            temporary = bridge.with_suffix(".so.tmp")
            try:
                with source, open(temporary, "wb") as handle:
                    shutil.copyfileobj(source, handle)
                temporary.chmod(0o755)
                temporary.replace(bridge)
            finally:
                temporary.unlink(missing_ok=True)

    if not bridge.is_file() or sha256_file(bridge) != release.member_sha256:
        fail("TrustTunnel Linux bridge archive did not contain the expected shared library")

    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    staged = SERVICES_DIR / bridge.name
    shutil.copyfile(bridge, staged)
    staged.chmod(0o755)
    log(f"Staged checksum-pinned TrustTunnel Linux bridge: {staged}")


def find_linux_libcxx_runtime() -> Path | None:
    """Find a workspace-local LLVM runtime suitable for the Linux bridge."""

    candidates = []
    configured = os.environ.get("DOBBYVPN_LIBCXX_RUNTIME")
    if configured:
        candidates.append(Path(configured))
    candidates.append(TOOLS_DIR / f"llvm-libcxx-{LLVM_LIBCXX_VERSION}")
    for runtime in candidates:
        if (runtime / "libc++.so").is_file() and (
            runtime / "libc++abi.so"
        ).is_file():
            return runtime
    return None


def install_linux_libcxx_runtime(skip_deps: bool) -> Path:
    """Stage pinned local C++ runtimes for linking and execution."""

    runtime = find_linux_libcxx_runtime()
    if runtime is None:
        if skip_deps:
            fail(
                "LLVM libc++ and libc++abi are required for the Linux "
                "TrustTunnel bridge"
            )
        if not command_exists("dpkg-deb"):
            fail("dpkg-deb is required to extract workspace-local LLVM runtimes")
        runtime = TOOLS_DIR / f"llvm-libcxx-{LLVM_LIBCXX_VERSION}"
        extract_dir = Path(tempfile.mkdtemp(prefix="dobby-libcxx-"))
        try:
            for filename, expected_digest in LLVM_LIBCXX_PACKAGES:
                archive = TOOLS_DIR / "downloads" / filename
                if not archive.exists():
                    download(
                        "https://apt.llvm.org/noble/pool/main/l/"
                        f"llvm-toolchain-21/{filename}",
                        archive,
                    )
                if hashlib.sha256(archive.read_bytes()).hexdigest() != expected_digest:
                    fail(f"LLVM runtime archive checksum mismatch: {filename}")
                run(["dpkg-deb", "-x", str(archive), str(extract_dir)])

            runtime.mkdir(parents=True, exist_ok=True)
            for library in ("libc++", "libc++abi"):
                matches = list(extract_dir.rglob(f"{library}.so.1.0"))
                if len(matches) != 1:
                    fail(f"LLVM runtime archive did not contain exactly one {library}")
                for name in (f"{library}.so", f"{library}.so.1"):
                    shutil.copyfile(matches[0], runtime / name)
                    (runtime / name).chmod(0o755)
        finally:
            shutil.rmtree(extract_dir, ignore_errors=True)
        runtime = find_linux_libcxx_runtime()
    if runtime is None:
        fail("Workspace-local LLVM runtime bootstrap did not produce required files")

    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    for name in ("libc++.so.1", "libc++abi.so.1"):
        source = runtime / name
        if not source.exists():
            fail(f"Workspace-local LLVM runtime is missing {name}")
        for directory in (GO_MODULE_DIR, SERVICES_DIR):
            target = directory / name
            shutil.copyfile(source.resolve(), target)
            target.chmod(0o755)
    log("Staged pinned workspace-local libc++ and libc++abi runtimes")
    return runtime


def ensure_build_dependencies(target_platform: str, skip_deps: bool, need_android: bool) -> None:
    install_linux_packages(skip_deps)
    install_go(skip_deps)
    configure_go_module_proxy()
    ensure_compiler(target_platform, skip_deps)
    if need_android:
        install_jdk(skip_deps)
        install_android_sdk(skip_deps)


def go_mod_download(run_tidy: bool) -> None:
    if run_tidy:
        run(["go", "mod", "tidy"], cwd=GO_MODULE_DIR)
    run(["go", "mod", "download"], cwd=GO_MODULE_DIR)


def prepare_go_test_dependencies(skip_deps: bool, run_go_mod_tidy: bool) -> None:
    """Materialize the exact native closure required by Linux Go tests.

    The pinned go-go-tunnel module embeds its Linux cgo search path in the
    module cache. The public bridge and libc++ runtimes are therefore staged
    in the checkout and added through CGO_LDFLAGS/LD_LIBRARY_PATH before the
    test process starts. This keeps hosted CI on the same dependency contract
    as the desktop service build without compiling a service as a side effect.
    """
    if host_platform() != "linux":
        fail("prepare-go-test-deps is supported only on Linux CI runners")

    ensure_build_dependencies("linux", skip_deps, need_android=False)
    install_linux_trusttunnel_bridge(skip_deps)
    runtime = install_linux_libcxx_runtime(skip_deps)
    go_mod_download(run_go_mod_tidy)

    environment = os.environ.copy()
    append_cgo_ldflags(
        environment,
        f"-L{GO_MODULE_DIR}",
        f"-L{runtime}",
        "-Wl,--no-as-needed",
    )
    set_env("CGO_ENABLED", "1")
    set_env("CGO_LDFLAGS", environment["CGO_LDFLAGS"])
    existing_library_path = os.environ.get("LD_LIBRARY_PATH", "").strip()
    library_path = os.pathsep.join(
        part for part in (str(GO_MODULE_DIR), str(runtime), existing_library_path) if part
    )
    set_env("LD_LIBRARY_PATH", library_path)
    log(f"Prepared Linux Go-test native dependencies with CGO_LDFLAGS={environment['CGO_LDFLAGS']}")
    log(f"Prepared Linux Go-test runtime path: {library_path}")


def service_output_path(target_platform: str) -> Path:
    return GO_MODULE_DIR / SERVICE_NAMES[target_platform]


def service_target_path(target_platform: str) -> Path:
    return SERVICES_DIR / SERVICE_NAMES[target_platform]


def build_cli(target_platform: str, arch: str | None = None) -> Path:
    """Build the native operator CLI without invoking the JVM launcher."""
    target_arch = arch or default_service_arch(target_platform)
    output = GO_MODULE_DIR / CLI_NAMES[target_platform]
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": GOOS_BY_PLATFORM[target_platform], "GOARCH": target_arch})
    ldflags = "-buildid="
    if target_platform == "macos":
        # Go's internal Darwin linker emits a macOS 12 load command even when
        # the app declares macOS 11 support.  Use the host external linker so
        # the native CLI remains honest about the product's minimum version.
        env["CGO_ENABLED"] = "1"
        ldflags += f" -linkmode=external -extldflags=-mmacosx-version-min={MACOS_MINIMUM_SYSTEM_VERSION}"
    run(
        ["go", "build", "-trimpath", f"-ldflags={ldflags}", "-o", output.name, "./cmd/dobbyvpn/"],
        cwd=GO_MODULE_DIR,
        env=env,
    )
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    target = SERVICES_DIR / CLI_NAMES[target_platform]
    shutil.copyfile(output, target)
    if target_platform != "windows":
        target.chmod(target.stat().st_mode | 0o111)
    log(f"Built native Go CLI {target}")
    return target


def install_macos_amd64_trusttunnel_helper(skip_deps: bool) -> None:
    """Stage the pinned official helper beside the Intel macOS service.

    The in-process bridge remains the arm64 implementation. Intel macOS uses
    this separate universal executable so it never links the arm64-only
    go-go-tunnel archive.
    """
    if skip_deps:
        helper = GO_MODULE_DIR / "trusttunnel_client"
        if not helper.exists():
            fail("official TrustTunnelClient helper is required for macOS amd64")
    archive = TOOLS_DIR / "downloads" / f"trusttunnel_client-v{TRUSTTUNNEL_MACOS_VERSION}-macos-universal.tar.gz"
    if not archive.exists() and not skip_deps:
        download(
            "https://github.com/TrustTunnel/TrustTunnelClient/releases/download/"
            f"v{TRUSTTUNNEL_MACOS_VERSION}/trusttunnel_client-v{TRUSTTUNNEL_MACOS_VERSION}-macos-universal.tar.gz",
            archive,
        )
    if archive.exists():
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        if digest != TRUSTTUNNEL_MACOS_ARCHIVE_SHA256:
            fail("official TrustTunnelClient archive checksum mismatch")
        with tarfile.open(archive, "r:gz") as tar_file:
            member_name = f"trusttunnel_client-v{TRUSTTUNNEL_MACOS_VERSION}-macos-universal/trusttunnel_client"
            try:
                member = tar_file.getmember(member_name)
            except KeyError:
                fail("official TrustTunnelClient archive did not contain the helper")
            if not member.isfile():
                fail("official TrustTunnelClient helper archive member is invalid")
            source = tar_file.extractfile(member)
            if source is None:
                fail("official TrustTunnelClient helper could not be extracted")
            target = GO_MODULE_DIR / "trusttunnel_client"
            with source, open(target, "wb") as handle:
                shutil.copyfileobj(source, handle)
            target.chmod(0o755)
    helper = GO_MODULE_DIR / "trusttunnel_client"
    if not helper.exists():
        fail("official TrustTunnelClient helper is unavailable")
    SERVICES_DIR.mkdir(parents=True, exist_ok=True)
    staged = SERVICES_DIR / "trusttunnel_client"
    shutil.copyfile(helper, staged)
    staged.chmod(0o755)
    log(f"Staged pinned TrustTunnelClient helper beside Intel macOS service: {staged}")


def default_service_arch(target_platform: str) -> str:
    if os.environ.get("GITHUB_ACTIONS") == "true":
        return CI_ARCH_BY_PLATFORM[target_platform]
    if target_platform == host_platform():
        return go_arch_from_machine()
    return CI_ARCH_BY_PLATFORM[target_platform]


def build_service(
    target_platform: str,
    arch: str | None,
    skip_deps: bool,
    skip_build: bool,
    run_go_mod_tidy: bool,
    *,
    build_tags: tuple[str, ...] = (),
    output_path: Path | None = None,
    runtime_dir: Path | None = None,
) -> Path:
    target_arch = arch or default_service_arch(target_platform)
    ensure_build_dependencies(target_platform, skip_deps, need_android=False)
    if target_platform == "windows":
        # The service imports the bridge and loads Wintun at process startup.
        # Stage exactly that runtime closure for the public artifact and local CLI.
        install_wintun(skip_deps)
        install_windows_bridge(skip_deps)
    if target_platform == "linux":
        if target_arch != "amd64":
            fail("The pinned TrustTunnel Linux bridge currently supports amd64 only")
        if runtime_dir is None:
            install_linux_trusttunnel_bridge(skip_deps)
            linux_libcxx_runtime = install_linux_libcxx_runtime(skip_deps)
        else:
            if runtime_dir.is_symlink():
                fail("The supplied Linux runtime directory is invalid")
            runtime_dir = runtime_dir.resolve()
            if not runtime_dir.is_dir() or runtime_dir.is_symlink():
                fail("The supplied Linux runtime directory is invalid")
            for name in ("libdobby_bridge.so", "libc++.so.1", "libc++abi.so.1"):
                candidate = runtime_dir / name
                if not candidate.is_file() or candidate.is_symlink():
                    fail(f"The supplied Linux runtime directory is missing {name}")
            linux_libcxx_runtime = runtime_dir
    else:
        linux_libcxx_runtime = None
    go_mod_download(run_go_mod_tidy)

    output = output_path.resolve() if output_path is not None else service_output_path(target_platform)
    if output_path is not None:
        if output.exists() or output.is_symlink():
            fail("The requested service output already exists")
        output.parent.mkdir(parents=True, exist_ok=True)
    if skip_build and output.exists():
        log(f"Reusing existing {output.name}")
    else:
        log(f"Building {target_platform} gRPC VPN service for {target_arch}")
        env = os.environ.copy()
        env.update(
            {
                "CGO_ENABLED": "1",
                "GOOS": GOOS_BY_PLATFORM[target_platform],
                "GOARCH": target_arch,
            }
        )
        ldflags = "-buildid="
        if target_platform == "macos":
            # Keep the Intel package's declared macOS 11 floor valid for both
            # the gRPC service and the native operator CLI.
            ldflags += f" -linkmode=external -extldflags=-mmacosx-version-min={MACOS_MINIMUM_SYSTEM_VERSION}"
        if target_platform == "linux":
            bridge_search_path = f"-L{GO_MODULE_DIR}"
            runtime_search_path = f"-L{linux_libcxx_runtime}"
            # DT_RPATH is intentional here: the pinned bridge has indirect
            # libc++ dependencies, and DT_RUNPATH is not transitive.
            origin_runpath = "-Wl,--disable-new-dtags,-rpath,$ORIGIN"
            retain_runtime_dependencies = "-Wl,--no-as-needed"
            append_cgo_ldflags(
                env,
                bridge_search_path,
                runtime_search_path,
                origin_runpath,
                retain_runtime_dependencies,
            )
        elif target_platform == "macos":
            # go-go-tunnel's static bridge contains C++ and uses the macOS
            # SystemConfiguration APIs. cgo does not infer either dependency
            # from a static archive.
            append_cgo_ldflags(env, "-lc++", "-framework", "SystemConfiguration")
        command = ["go", "build", "-trimpath"]
        if build_tags:
            command.append(f"-tags={','.join(build_tags)}")
        command.extend(
            [
                f"-ldflags={ldflags}",
                "-o",
                os.fspath(output),
                "./desktop_exports/",
            ]
        )
        run(
            command,
            cwd=GO_MODULE_DIR,
            env=env,
        )

    if output_path is not None:
        target = output
    else:
        SERVICES_DIR.mkdir(parents=True, exist_ok=True)
        target = service_target_path(target_platform)
        shutil.copyfile(output, target)
    if target_platform != "windows":
        target.chmod(target.stat().st_mode | 0o111)
    if output_path is None and target_platform == "macos" and target_arch == "amd64":
        install_macos_amd64_trusttunnel_helper(skip_deps)
    log(f"Copied {output.name} to {target}")
    return target


def read_gradle_properties() -> dict[str, str]:
    properties: dict[str, str] = {}
    for line in (KMP_DIR / "gradle.properties").read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        properties[key.strip()] = value.strip()
    return properties


def github_repo_from_remote() -> str | None:
    remote = run_capture(["git", "remote", "get-url", "origin"])
    if not remote or "github.com" not in remote:
        return None
    remote = remote.removesuffix(".git")
    if remote.startswith("git@github.com:"):
        return remote.removeprefix("git@github.com:")
    marker = "github.com/"
    if marker in remote:
        return remote.split(marker, 1)[1]
    return None


def desktop_version_properties() -> list[str]:
    gradle_properties = read_gradle_properties()
    major = os.environ.get("APP_MAJOR_VERSION")
    minor = os.environ.get("APP_MINOR_VERSION")
    maintenance = os.environ.get("APP_MAINTENANCE_VERSION")

    if major is not None and minor is not None and maintenance is not None:
        version_name = f"{major}.{minor}.{maintenance}"
    else:
        version_name = os.environ.get("VERSION_NAME") or gradle_properties.get("versionName", "0.0.1")

    # Prefer CI run number (same as Android/iOS APPLE_BUILD_NUMBER) so desktop
    # stays unique when marketing VERSION is held constant.
    version_code = (
        os.environ.get("APPLE_BUILD_NUMBER")
        or os.environ.get("VERSION_CODE")
        or os.environ.get("ANDROID_VERSION_CODE")
    )
    if version_code is None and major is not None and minor is not None and maintenance is not None:
        version_code = str(int(major) * 1_000_000 + int(minor) * 1_000 + int(maintenance))
    if version_code is None:
        version_code = gradle_properties.get("versionCode", "1")

    commit = os.environ.get("GITHUB_SHA") or run_capture(["git", "rev-parse", "HEAD"]) or "N/A"
    repo = os.environ.get("GITHUB_REPOSITORY") or github_repo_from_remote() or "DobbyVPN/DobbyVPN"
    commit_link = os.environ.get("PROJECT_REPOSITORY_COMMIT_LINK")
    if not commit_link:
        commit_link = "N/A" if commit == "N/A" else f"https://github.com/{repo}/tree/{commit}"

    return [
        f"-PprojectRepositoryCommit={commit}",
        f"-PprojectRepositoryCommitLink={commit_link}",
        f"-Pandroid.injected.version.code={version_code}",
        f"-Pandroid.injected.version.name={version_name}",
    ]


def gradle_command() -> str:
    if host_platform() == "windows":
        return str(KMP_DIR / "gradlew.bat")
    return "./gradlew"


def run_desktop_gradle(skip_deps: bool) -> None:
    install_jdk(skip_deps)
    install_android_sdk(skip_deps)

    props = desktop_version_properties()
    run([gradle_command(), "--build-cache", "--parallel", ":app:jvmJar", *props], cwd=KMP_DIR)
    run([gradle_command(), "--no-daemon", "dependencies", *props], cwd=KMP_DIR)
    run([gradle_command(), "--no-daemon", "printConveyorConfig", *props], cwd=KMP_DIR)


def emit_conveyor_config() -> None:
    """Print only Conveyor's generated HOCON on every supported host."""
    with contextlib.redirect_stdout(sys.stderr):
        install_jdk(skip_deps=False)
    command = [gradle_command(), "--no-daemon", "printConveyorConfig", *desktop_version_properties()]
    try:
        result = subprocess.run(
            command,
            cwd=str(KMP_DIR),
            env=os.environ.copy(),
            stdout=subprocess.PIPE,
            stderr=sys.stderr,
            text=True,
            check=False,
        )
    except FileNotFoundError as error:
        fail(f"Command was not found: {error.filename}")
    if result.returncode != 0:
        emit_process_diagnostic(
            f"[!] Conveyor config generation failed with exit code {result.returncode}",
            result.stdout,
        )
        fail(f"Conveyor config generation failed with exit code {result.returncode}")
    marker = "// Generated by the Conveyor Gradle plugin."
    marker_index = result.stdout.find(marker)
    if marker_index < 0:
        # Keep compatibility with older/custom Gradle tasks that already
        # return a configuration-only stream.  The real Conveyor task emits
        # the marker and takes the diagnostics-safe path below.
        sys.stdout.write(result.stdout)
        return
    # Gradle writes its lifecycle banner, warnings, and task output to stdout
    # alongside the task's generated HOCON.  Conveyor treats stdout as a
    # configuration-only protocol, so preserve the complete child stream as
    # diagnostics on stderr and pass only the marked configuration through.
    sys.stderr.write(result.stdout)
    config = result.stdout[marker_index:]
    for terminator in ("\n[Incubating] Problems report", "\nDeprecated Gradle features were used"):
        end_index = config.find(terminator)
        if end_index >= 0:
            config = config[:end_index]
    sys.stdout.write(config.rstrip() + "\n")


def required_service_platforms(require_all: bool, platform_value: str) -> list[str]:
    if require_all:
        return ["linux", "macos", "windows"]
    platforms = selected_platforms(platform_value)
    if platforms == ["linux", "macos", "windows"]:
        return platforms
    return platforms


def require_services(require_all: bool, platform_value: str) -> None:
    missing = []
    for target_platform in required_service_platforms(require_all, platform_value):
        target = service_target_path(target_platform)
        if not target.exists():
            missing.append(str(target))
            continue
        if target_platform != "windows":
            target.chmod(target.stat().st_mode | 0o111)
    if missing:
        fail("Missing service binaries:\n" + "\n".join(missing))


def run_conveyor(passphrase: str | None) -> None:
    conveyor = os.environ.get("CONVEYOR_CMD") or shutil.which("conveyor")
    if not conveyor:
        fail("Conveyor CLI was not found. Set CONVEYOR_CMD or run without --package.")
    command = [conveyor, "-f", str(KMP_DIR / "conveyor.conf")]
    if passphrase:
        command.extend((f"--passphrase={passphrase}", "make", "site"))
    else:
        command.extend(("make", "site"))
    run(command)


def build_app(args: argparse.Namespace) -> None:
    platforms = selected_platforms(args.platform)
    if not args.skip_libs:
        for target_platform in platforms:
            build_service(
                target_platform,
                args.arch,
                args.skip_deps,
                args.skip_build,
                args.go_mod_tidy,
            )

    # The native operator CLI is a packaging input, not a JVM application
    # output.  A source/package build may deliberately reuse already-built
    # service binaries via --skip-libs, but it must still materialize the CLI
    # for the selected target before Conveyor resolves conveyor.conf.
    for target_platform in platforms:
        cli_target = service_target_path(target_platform).parent / CLI_NAMES[target_platform]
        if not cli_target.exists():
            build_cli(target_platform, args.arch)

    if args.require_all_services:
        require_services(True, args.platform)
    run_desktop_gradle(args.skip_deps)
    if args.package:
        run_conveyor(args.conveyor_passphrase)


def build_test_seams_service(args: argparse.Namespace) -> None:
    """Build a private Linux hardening service without changing release inputs."""
    if args.platform != "linux":
        fail("The build-local health seam is supported only for Linux hardening")
    output = Path(args.output)
    if not output.is_absolute() or output.is_symlink() or output.exists():
        fail("Hardening service output must be an absent absolute path")
    runtime_dir = Path(args.runtime_dir)
    if not runtime_dir.is_absolute():
        fail("Hardening service runtime directory must be absolute")
    build_service(
        "linux",
        args.arch or "amd64",
        args.skip_deps,
        False,
        args.go_mod_tidy,
        build_tags=("dobbyvpn_test_seams",),
        output_path=output,
        runtime_dir=runtime_dir,
    )


def is_windows_admin() -> bool:
    if host_platform() != "windows":
        return True
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


def prepare_config_arg(config: str) -> str:
    if config.startswith("http://") or config.startswith("https://"):
        return config
    path = Path(config)
    if path.exists():
        return str(path)
    # A literal profile/config passed to the local CLI test is an owner-only
    # run artifact.  Allocate a fresh file instead of replacing a prior
    # config or diagnostic record at a fixed checkout path.
    descriptor, name = tempfile.mkstemp(prefix="dobbyvpn-cli-config-", suffix=".toml")
    config_path = Path(name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            descriptor = -1
            handle.write(config)
            handle.flush()
            os.fsync(handle.fileno())
        config_path.chmod(0o600)
    finally:
        if descriptor != -1:
            os.close(descriptor)
    return str(config_path)


def wait_for_port(port: int, timeout_seconds: int = 30) -> bool:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return True
        except OSError:
            time.sleep(1)
    return False


def wait_for_socket(path: Path, timeout_seconds: int = 30) -> bool:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        try:
            if stat.S_ISSOCK(path.stat().st_mode):
                return True
        except FileNotFoundError:
            pass
        time.sleep(1)
    return False


def sudo_prefix() -> list[str]:
    if host_platform() == "windows":
        return []
    if hasattr(os, "geteuid") and os.geteuid() == 0:
        return []
    return ["sudo"]


def open_service_log(kind: str, *, binary: bool = False):
    """Open a unique owner-only service log without replacing prior evidence."""
    directory = ROOT_DIR / "runtime" / "desktop-build-diagnostics"
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory.chmod(0o700)
    handle = tempfile.NamedTemporaryFile(
        mode="wb" if binary else "w",
        **({} if binary else {"encoding": "utf-8"}),
        prefix=f"grpcvpnserver-{kind}-",
        suffix=".log",
        dir=directory,
        delete=False,
    )
    Path(handle.name).chmod(0o600)
    return handle


def close_service_logs(handles: list[object]) -> None:
    for handle in handles:
        close = getattr(handle, "close", None)
        if close:
            close()


def start_service(
    target_platform: str,
    port: int,
    control_socket: Path | None = None,
) -> tuple[subprocess.Popen[str], list[object]]:
    service = service_target_path(target_platform)
    if not service.exists():
        fail(f"Missing service binary: {service}")

    handles: list[object] = []
    if target_platform == "windows":
        stdout = open_service_log("out")
        stderr = open_service_log("err")
        command = [str(service), "-port", str(port)]
        environment = os.environ.copy()
    else:
        stdout = open_service_log("combined")
        stderr = subprocess.STDOUT
        if control_socket is None:
            fail("A private control socket path is required for Unix CLI tests")
        environment = os.environ.copy()
        environment["DOBBYVPN_CONTROL_SOCKET"] = str(control_socket)
        prefix = sudo_prefix()
        command = [*prefix]
        if prefix:
            command.extend(["env", f"DOBBYVPN_CONTROL_SOCKET={control_socket}"])
        command.extend([str(service), "-port", str(port)])
    handles.append(stdout)
    if hasattr(stderr, "close"):
        handles.append(stderr)

    log("Starting VPN control service")
    process = subprocess.Popen(
        command,
        cwd=str(ROOT_DIR),
        env=environment,
        stdout=stdout,
        stderr=stderr,
        text=True,
        **process_group_options(),
    )
    process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
    attach_process_tree_tracker(process)
    ready = wait_for_port(port) if target_platform == "windows" else wait_for_socket(control_socket)
    if ready:
        log("gRPC VPN service is ready")
        return process, handles

    stop_service(process)
    close_service_logs(handles)
    print_service_logs(handles)
    fail("gRPC VPN service did not become ready")


def stop_service(process: subprocess.Popen[str]) -> None:
    if process.poll() is None:
        log("Stopping gRPC VPN service")
    try:
        tree_proof = terminate_process_group(process)
    except ProcessTreeProofError as error:
        retain_process_diagnostics("service-tree", None, str(error), "stop-failed")
        raise
    retain_process_diagnostics("service-tree", None, tree_proof, "stopped")


def print_service_logs(handles: list[object]) -> None:
    for handle in handles:
        name = getattr(handle, "name", None)
        if not isinstance(name, (str, os.PathLike)):
            continue
        path = Path(name)
        try:
            output = path.read_bytes()
        except OSError as error:
            emit_process_diagnostic(f"[!] Could not read service log {path.name}: {error}")
            continue
        if public_actions():
            emit_public_diagnostic("desktop-service", (f"service={path.name}\n", output), root_dir=ROOT_DIR)
            continue
        print(f"--- {path.name} ---")
        rendered = output.decode("utf-8", errors="replace")
        sys.stdout.write(rendered)
        if output and not output.endswith(b"\n"):
            sys.stdout.write("\n")
        sys.stdout.flush()


def remove_control_socket_parent(control_socket: Path | None) -> None:
    if control_socket is None:
        return
    try:
        socket_mode = control_socket.lstat().st_mode
    except FileNotFoundError:
        socket_mode = None
    if socket_mode is not None:
        if not stat.S_ISSOCK(socket_mode):
            log("Refusing to remove a non-socket control path")
            return
        run([*sudo_prefix(), "unlink", str(control_socket)], check=False)
    run([*sudo_prefix(), "rmdir", str(control_socket.parent)], check=False)


def run_cli_check(config_arg: str, port: int, control_socket: Path | None = None) -> None:
    env = os.environ.copy()
    env["PORT"] = str(port)
    if control_socket is not None:
        env["DOBBYVPN_CONTROL_SOCKET"] = str(control_socket)
    target = SERVICES_DIR / CLI_NAMES[host_platform()]
    run([str(target), "check-config", config_arg], cwd=KMP_DIR, env=env)


def cli_test(args: argparse.Namespace) -> None:
    config = args.config or os.environ.get("DOBBYVPN_CLI_TEST_CONFIG")
    if not config:
        fail("Pass --config <url-or-file> or set DOBBYVPN_CLI_TEST_CONFIG")
    if not is_windows_admin():
        fail("Run this command from an elevated shell so the VPN service can configure Wintun")

    target_platform = host_platform()
    ensure_build_dependencies(target_platform, args.skip_deps, need_android=True)
    if target_platform == "windows":
        install_wintun(args.skip_deps)

    if not args.skip_build:
        build_service(
            target_platform,
            go_arch_from_machine(),
            args.skip_deps,
            skip_build=False,
            run_go_mod_tidy=args.go_mod_tidy,
        )
        build_cli(target_platform, go_arch_from_machine())
        run_desktop_gradle(args.skip_deps)
    else:
        require_services(False, "current")
        if not (SERVICES_DIR / CLI_NAMES[target_platform]).exists():
            build_cli(target_platform, go_arch_from_machine())

    config_arg = prepare_config_arg(config)
    process: subprocess.Popen[str] | None = None
    handles: list[object] = []
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-cli-control-") as control_root:
        control_socket = (
            None
            if target_platform == "windows"
            else Path(control_root) / "service" / "control.sock"
        )
        try:
            process, handles = start_service(target_platform, args.port, control_socket)
            run_cli_check(config_arg, args.port, control_socket)
        finally:
            if process:
                stop_service(process)
            remove_control_socket_parent(control_socket)
            close_service_logs(handles)
            print_service_logs(handles)


def add_common_options(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--skip-deps", action="store_true", help="Do not install missing local dependencies.")
    parser.add_argument("--skip-build", action="store_true", help="Reuse existing build outputs when possible.")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build DobbyVPN desktop services/app in the same shape used by CI."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    libs = subparsers.add_parser("libs", help="Build desktop gRPC VPN service binaries.")
    add_common_options(libs)
    libs.add_argument("--platform", default="current", help="current, linux, macos, windows, ubuntu, or all.")
    libs.add_argument("--arch", help="Override GOARCH for the service build.")
    libs.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before go mod download.")

    go_test_deps = subparsers.add_parser(
        "prepare-go-test-deps",
        help="Stage pinned Linux native dependencies and environment for Go tests.",
    )
    add_common_options(go_test_deps)
    go_test_deps.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before go mod download.")

    app = subparsers.add_parser("app", help="Build the desktop JVM app and Conveyor config.")
    add_common_options(app)
    app.add_argument(
        "--platform",
        default="current",
        help="Service platform to build/copy when --skip-libs is not set.",
    )
    app.add_argument("--arch", help="Override GOARCH for service builds.")
    app.add_argument("--skip-libs", action="store_true", help="Use existing kmp_module/services binaries.")
    app.add_argument("--require-all-services", action="store_true", help="Require Linux, macOS, and Windows services.")
    app.add_argument("--package", action="store_true", help="Run local Conveyor packaging after the Gradle build.")
    app.add_argument("--conveyor-passphrase", default=os.environ.get("CONVEYOR_PASSPHRASE"))
    app.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before service builds.")

    subparsers.add_parser(
        "conveyor-config",
        help="Emit generated Conveyor HOCON without platform-specific wrapper assumptions.",
    )

    cli = subparsers.add_parser("cli-test", help="Build current desktop target and run check-config.")
    add_common_options(cli)
    cli.add_argument("--config", help="Config URL, TOML file path, or inline TOML.")
    cli.add_argument("--port", type=int, default=int(os.environ.get("PORT", "50151")))
    cli.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before the service build.")

    test_seams = subparsers.add_parser(
        "test-seams-service",
        help="Build the private Linux hardening service with explicit test seams.",
    )
    test_seams.add_argument("--skip-deps", action="store_true", help="Do not install missing local dependencies.")
    test_seams.add_argument("--platform", default="linux")
    test_seams.add_argument("--arch", default="amd64")
    test_seams.add_argument("--output", required=True, help="Absent absolute service output path.")
    test_seams.add_argument("--runtime-dir", required=True, help="Installed Linux runtime library directory.")
    test_seams.add_argument("--go-mod-tidy", action="store_true", help="Run go mod tidy before the service build.")

    return parser.parse_args()


def main() -> None:
    if not GO_MODULE_DIR.is_dir() or not KMP_DIR.is_dir():
        fail("Run this script from a cloned DobbyVPN repository")
    bootstrap_local_tools()
    args = parse_args()

    if args.command == "libs":
        for target_platform in selected_platforms(args.platform):
            build_service(
                target_platform,
                args.arch,
                args.skip_deps,
                args.skip_build,
                args.go_mod_tidy,
            )
    elif args.command == "prepare-go-test-deps":
        prepare_go_test_dependencies(args.skip_deps, args.go_mod_tidy)
    elif args.command == "app":
        build_app(args)
    elif args.command == "cli-test":
        cli_test(args)
    elif args.command == "test-seams-service":
        build_test_seams_service(args)
    elif args.command == "conveyor-config":
        emit_conveyor_config()
        return
    else:
        fail(f"Unknown command: {args.command}")

    log("Done")


if __name__ == "__main__":
    main()
