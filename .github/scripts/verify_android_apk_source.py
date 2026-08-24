#!/usr/bin/env python3
"""Verify the source commit embedded in a built DobbyVPN APK."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import threading
import time

from public_output import emit_diagnostic as emit_public_diagnostic
from public_output import public_actions


SHA40 = re.compile(r"[0-9a-f]{40}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
BUILD_CONFIG = "com.dobby.vpn.BuildConfig"
ROOT_DIR = Path(__file__).resolve().parents[2]
APK_ANALYZER_TIMEOUT_SECONDS = 120
PROCESS_CLEANUP_GRACE_SECONDS = 5
PROCESS_TREE_POLL_INTERVAL_SECONDS = 0.01


class VerificationError(ValueError):
    pass


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
    except FileNotFoundError:
        return None
    except OSError as error:
        raise ProcessTreeProofError(
            f"could not read process identity pid={pid}: {error}"
        ) from error
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
        except (FileNotFoundError, ProcessLookupError):
            continue
        except OSError as error:
            raise ProcessTreeProofError(
                f"could not read process-group census {stat_path}: {error}"
            ) from error
        closing_parenthesis = stat_text.rfind(")")
        fields = stat_text[closing_parenthesis + 2 :].split()
        if len(fields) <= 2:
            raise ProcessTreeProofError(
                f"invalid process-group census record {stat_path}"
            )
        if fields[0] == "Z":
            continue
        try:
            process_group = int(fields[2])
            pid = int(stat_text[: stat_text.find(" ")])
        except (ValueError, TypeError) as error:
            raise ProcessTreeProofError(
                f"invalid process-group census record {stat_path}: {error}"
            ) from error
        if process_group == group_id:
            members.append(pid)
    return members


def _pid_is_alive(pid: int, expected_identity: tuple[str, str] | None = None) -> bool:
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
        timeout=2,
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


def _windows_descendants(root_pid: int) -> set[int]:
    command = [
        "powershell.exe",
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        "Get-CimInstance Win32_Process | "
        "Select-Object ProcessId,ParentProcessId | ConvertTo-Json -Compress",
    ]
    result = subprocess.run(
        command,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=False,
        timeout=2,
    )
    if result.returncode != 0:
        raise ProcessTreeProofError(
            f"PowerShell process-tree query failed exit={result.returncode} "
            f"stderr={output_text(result.stderr).strip()}"
        )
    if result.stderr:
        raise ProcessTreeProofError(
            "PowerShell process-tree query emitted stderr="
            + output_text(result.stderr).strip()
        )
    try:
        records = json.loads(output_text(result.stdout) or "[]")
    except json.JSONDecodeError as error:
        raise ProcessTreeProofError("PowerShell process-tree query returned invalid JSON") from error
    if isinstance(records, dict):
        records = [records]
    children_by_parent: dict[int, set[int]] = {}
    for record in records:
        if not isinstance(record, dict):
            continue
        try:
            pid = int(record["ProcessId"])
            parent_pid = int(record["ParentProcessId"])
        except (KeyError, TypeError, ValueError):
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


def _process_tree_snapshot(root_pid: int) -> tuple[set[int], str]:
    if os.name == "nt":
        return _windows_descendants(root_pid), "powershell-cim"
    if Path("/proc").is_dir():
        return _proc_descendants(root_pid), "procfs"
    return _ps_descendants(root_pid), "ps"


class ProcessTreeTracker:
    def __init__(self, root_pid: int) -> None:
        self.root_pid = root_pid
        self.observed: set[int] = {root_pid}
        self.identities: dict[int, tuple[str, str]] = {}
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
            descendants, source = _process_tree_snapshot(self.root_pid)
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
                raise ProcessTreeProofError(
                    f"could not signal descendant pid={pid} value={signum}: {error}"
                ) from error

    def observed_pids(self) -> tuple[int, ...]:
        with self._lock:
            return tuple(sorted(self.observed))

    def prove_gone(self, group_id: int) -> str:
        self._stop.set()
        self._thread.join(timeout=2)
        if self._thread.is_alive():
            raise ProcessTreeProofError("process-tree watcher did not stop")
        self._sample()
        with self._lock:
            error = self.error
            observed = tuple(sorted(self.observed))
            source = self.source
        if error is not None:
            raise error
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


def emit_process_diagnostic(*outputs: str | bytes | None) -> None:
    if public_actions():
        emit_public_diagnostic("android-apk-source", outputs, root_dir=ROOT_DIR)
        return
    for output in outputs:
        if not output:
            continue
        if isinstance(output, bytes):
            output = output.decode("utf-8", errors="replace")
        sys.stderr.write(output)
        if not output.endswith("\n"):
            sys.stderr.write("\n")
    sys.stderr.flush()


def process_group_options() -> dict[str, int | bool]:
    if os.name == "nt":
        return {"creationflags": getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0)}
    return {"start_new_session": True}


def retain_process_diagnostics(
    stdout: str | bytes | None,
    stderr: str | bytes | None,
    status: str,
) -> Path:
    directory = ROOT_DIR / "runtime" / "android-apk-source-diagnostics"
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    directory.chmod(0o700)
    handle = tempfile.NamedTemporaryFile(
        mode="wb",
        prefix="apkanalyzer-",
        suffix=".log",
        dir=directory,
        delete=False,
    )
    Path(handle.name).chmod(0o600)
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
    tracker = process_tree_tracker(process)
    group_id = getattr(process, "_dobby_process_group_id", process.pid)
    if os.name == "nt":
        try:
            process.send_signal(signal.CTRL_BREAK_EVENT)
        except (AttributeError, ValueError) as error:
            raise ProcessTreeProofError(f"could not signal analyzer process: {error}") from error
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessTreeProofError(f"could not signal analyzer process: {error}") from error
    else:
        try:
            os.killpg(group_id, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessTreeProofError(
                f"could not terminate analyzer process group={group_id}: {error}"
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
            cleanup = subprocess.run(
                ["taskkill", "/T", "/F", "/PID", str(process.pid)],
                check=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=False,
                timeout=grace_seconds,
            )
            retain_process_diagnostics(
                cleanup.stdout,
                cleanup.stderr,
                f"taskkill-exit-{cleanup.returncode}",
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
                    descendant_cleanup.stdout,
                    descendant_cleanup.stderr,
                    f"taskkill-pid-{pid}-exit-{descendant_cleanup.returncode}",
                )
        except (OSError, subprocess.SubprocessError) as error:
            emit_process_diagnostic(str(error))
        try:
            process.kill()
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessTreeProofError(f"could not kill analyzer process: {error}") from error
    else:
        tracker.signal_descendants(signal.SIGKILL)
        try:
            os.killpg(group_id, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except OSError as error:
            raise ProcessTreeProofError(
                f"could not kill analyzer process group={group_id}: {error}"
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


def run_apkanalyzer(command: list[str]) -> subprocess.CompletedProcess[str]:
    process = subprocess.Popen(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=False,
        **process_group_options(),
    )
    process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
    attach_process_tree_tracker(process)
    try:
        stdout, stderr = process.communicate(timeout=APK_ANALYZER_TIMEOUT_SECONDS)
    except subprocess.TimeoutExpired as error:
        partial_stdout = getattr(error, "stdout", None) or getattr(error, "output", None)
        partial_stderr = getattr(error, "stderr", None)
        captured_stdout = partial_stdout or b""
        captured_stderr = partial_stderr or b""
        try:
            tree_proof = terminate_process_group(process)
        except ProcessTreeProofError as error:
            tree_proof = f"tree-proof-failed={error}"
            retain_process_diagnostics(captured_stdout, captured_stderr, f"timeout-{APK_ANALYZER_TIMEOUT_SECONDS}s;{tree_proof}")
            raise
        try:
            stdout, stderr = process.communicate(timeout=PROCESS_CLEANUP_GRACE_SECONDS)
        except subprocess.TimeoutExpired as cleanup_error:
            stdout = getattr(cleanup_error, "stdout", None) or partial_stdout or ""
            stderr = getattr(cleanup_error, "stderr", None) or partial_stderr or ""
        retain_process_diagnostics(stdout, stderr, f"timeout-{APK_ANALYZER_TIMEOUT_SECONDS}s;{tree_proof}")
        stdout_text = output_text(stdout)
        stderr_text = output_text(stderr)
        timeout_error = subprocess.TimeoutExpired(
            command,
            APK_ANALYZER_TIMEOUT_SECONDS,
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
                stdout,
                stderr,
                f"exit-{process.returncode};tree-proof-failed={error};cleanup-failed={cleanup_error}",
            )
            raise
    retain_process_diagnostics(stdout, stderr, f"exit-{process.returncode};{tree_proof}")
    return subprocess.CompletedProcess(
        command,
        process.returncode,
        output_text(stdout),
        output_text(stderr),
    )


def dex_string(code: str, field: str) -> str:
    pattern = re.compile(
        rf'^\.field public static final {re.escape(field)}:Ljava/lang/String; = "([^"]*)"$',
        re.MULTILINE,
    )
    values = pattern.findall(code)
    if len(values) != 1:
        raise VerificationError(f"APK BuildConfig must contain exactly one {field} value")
    return values[0]


def verify_code(code: str, source_sha: str, repository: str) -> None:
    if not SHA40.fullmatch(source_sha):
        raise VerificationError("source SHA must be full lowercase hexadecimal")
    if not REPOSITORY.fullmatch(repository):
        raise VerificationError("repository must be OWNER/NAME")
    expected_link = f"https://github.com/{repository}/tree/{source_sha}"
    if dex_string(code, "PROJECT_REPOSITORY_COMMIT") != source_sha:
        raise VerificationError("APK embedded source commit does not match selected source")
    if dex_string(code, "PROJECT_REPOSITORY_COMMIT_LINK") != expected_link:
        raise VerificationError("APK embedded source link does not match selected source")


def verify_apk(apkanalyzer: str, apk: Path, source_sha: str, repository: str) -> None:
    if apk.is_symlink() or not apk.is_file() or apk.stat().st_size <= 0:
        raise VerificationError("APK must be a nonempty regular file")
    command = [apkanalyzer, "dex", "code", "--class", BUILD_CONFIG, str(apk)]
    try:
        result = run_apkanalyzer(command)
    except subprocess.TimeoutExpired as error:
        emit_process_diagnostic(error.stdout or error.output, error.stderr)
        raise VerificationError(
            f"apkanalyzer timed out after {APK_ANALYZER_TIMEOUT_SECONDS} seconds",
        ) from error
    if result.returncode != 0:
        emit_process_diagnostic(result.stdout, result.stderr)
        raise VerificationError(
            f"apkanalyzer could not read APK BuildConfig (exit code {result.returncode})",
        )
    if result.stderr:
        emit_process_diagnostic(result.stderr)
    verify_code(result.stdout, source_sha, repository)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apk", action="append", required=True, type=Path)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--apkanalyzer", default="apkanalyzer")
    args = parser.parse_args()
    try:
        for apk in args.apk:
            verify_apk(args.apkanalyzer, apk, args.source_sha, args.repository)
    except (OSError, subprocess.SubprocessError, VerificationError) as error:
        print(f"Android APK source verification failed: {error}", file=sys.stderr)
        return 1
    print(f"Android APK embedded source verified for {len(args.apk)} artifact(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
