#!/usr/bin/env python3
"""Verify the source commit embedded in a built DobbyVPN APK."""

from __future__ import annotations

import argparse
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
from windows_process_census import WindowsProcessCensusError, windows_process_census


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


def _merge_output_fragments(*outputs: str | bytes | None) -> bytes:
    """Merge cumulative or incremental subprocess output without duplicating it."""
    merged = b""
    for output in outputs:
        data = output_bytes(output)
        if not data:
            continue
        if not merged:
            merged = data
            continue
        # TimeoutExpired exposes ``output`` as an alias for ``stdout``.  A
        # second communicate() can also return the cumulative stream, while
        # test doubles and alternate implementations may return only a new
        # suffix.  Handle all three forms without dropping bytes.
        if data == merged:
            continue
        if data.startswith(merged):
            merged = data
            continue
        # Independent partial reads are appended in order.  The only
        # duplicate forms produced by subprocess APIs are the stdout/output
        # aliases and cumulative snapshots handled above; do not scan large
        # diagnostics byte-by-byte looking for an arbitrary overlap.
        merged += data
    return merged


def _exception_output(error: BaseException) -> tuple[bytes, bytes]:
    """Return all partial streams exposed by a subprocess exception once."""
    stdout = _merge_output_fragments(
        getattr(error, "stdout", None),
        getattr(error, "output", None),
    )
    stderr = _merge_output_fragments(getattr(error, "stderr", None))
    return stdout, stderr


def _set_exception_output(error: BaseException, stdout: bytes, stderr: bytes) -> None:
    """Make merged streams available to callers handling the original error."""
    try:
        error.stdout = output_text(stdout)  # type: ignore[attr-defined]
        error.output = output_text(stdout)  # type: ignore[attr-defined]
        error.stderr = output_text(stderr)  # type: ignore[attr-defined]
    except (AttributeError, TypeError):
        # Some foreign exception implementations expose read-only attributes.
        pass


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
    if os.name == "nt":
        try:
            _, active_pids = windows_process_census(pid, timeout_seconds=2)
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
    try:
        result = subprocess.run(
            ["ps", "-eo", "pid=,ppid="],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=False,
            timeout=2,
        )
    except (subprocess.TimeoutExpired, OSError) as error:
        stdout, stderr = _exception_output(error)
        retain_process_diagnostics(
            stdout,
            stderr,
            f"ps-query-error={type(error).__name__};evidence_incomplete=1",
        )
        if isinstance(error, subprocess.TimeoutExpired):
            detail = "timed out after 2s"
        else:
            detail = f"could not start: {error}"
        raise ProcessTreeProofError(
            f"ps process-tree query {detail} stdout={output_text(stdout).strip()} "
            f"stderr={output_text(stderr).strip()} evidence_incomplete=1"
        ) from error
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
        return windows_process_census(root_pid, timeout_seconds=2)
    except WindowsProcessCensusError as error:
        raise ProcessTreeProofError(str(error)) from error


def _process_tree_snapshot(root_pid: int) -> tuple[set[int], str, set[int] | None]:
    if os.name == "nt":
        descendants, active_pids = _windows_process_snapshot(root_pid)
        return descendants, "windows-toolhelp", active_pids
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


def _run_windows_taskkill(
    pid: int,
    *,
    status_prefix: str,
    timeout_seconds: float,
) -> None:
    """Run taskkill while retaining partial output if the command fails."""
    try:
        result = subprocess.run(
            ["taskkill", "/T", "/F", "/PID", str(pid)],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=False,
            timeout=timeout_seconds,
        )
    except (OSError, subprocess.SubprocessError) as error:
        stdout, stderr = _exception_output(error)
        retain_process_diagnostics(
            stdout,
            stderr,
            f"{status_prefix}-error={type(error).__name__};evidence_incomplete=1",
        )
        emit_process_diagnostic(
            f"[!] Windows descendant cleanup failed for pid={pid}: {error}",
            stdout,
            stderr,
        )
        return
    retain_process_diagnostics(
        result.stdout,
        result.stderr,
        f"{status_prefix}-exit-{result.returncode}",
    )


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
        _run_windows_taskkill(
            process.pid,
            status_prefix="taskkill",
            timeout_seconds=grace_seconds,
        )
        for pid in tracker.observed_pids():
            if pid == process.pid:
                continue
            _run_windows_taskkill(
                pid,
                status_prefix=f"taskkill-pid-{pid}",
                timeout_seconds=grace_seconds,
            )
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


def _final_drain_and_reap(
    process: subprocess.Popen[str],
    stdout: bytes,
    stderr: bytes,
    *,
    grace_seconds: float,
) -> tuple[bytes, bytes, bool, str | None]:
    """Drain child pipes once, then boundedly kill and reap on drain failure."""
    try:
        drained_stdout, drained_stderr = process.communicate(timeout=grace_seconds)
    except (subprocess.TimeoutExpired, OSError) as error:
        partial_stdout, partial_stderr = _exception_output(error)
        stdout = _merge_output_fragments(stdout, partial_stdout)
        stderr = _merge_output_fragments(stderr, partial_stderr)
        cleanup_error: str | None = f"final-drain={type(error).__name__}: {error}"
        try:
            process.kill()
        except ProcessLookupError:
            pass
        except OSError as error:
            cleanup_error += f";kill={error}"
        try:
            process.wait(timeout=grace_seconds)
        except subprocess.TimeoutExpired as error:
            cleanup_error += f";reap={error}"
        except OSError as error:
            cleanup_error += f";reap={error}"
        return stdout, stderr, False, cleanup_error
    stdout = _merge_output_fragments(stdout, drained_stdout)
    stderr = _merge_output_fragments(stderr, drained_stderr)
    try:
        process.wait(timeout=grace_seconds)
    except subprocess.TimeoutExpired as error:
        return stdout, stderr, False, f"reap={error}"
    except OSError as error:
        return stdout, stderr, False, f"reap={error}"
    return stdout, stderr, True, None


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
    except (subprocess.TimeoutExpired, OSError) as error:
        captured_stdout, captured_stderr = _exception_output(error)
        tree_proof: str | None = None
        tree_error: ProcessTreeProofError | None = None
        try:
            tree_proof = terminate_process_group(process)
        except ProcessTreeProofError as cleanup_error:
            tree_error = cleanup_error
            tree_proof = f"tree-proof-failed={cleanup_error}"
        stdout, stderr, eof_proven, drain_error = _final_drain_and_reap(
            process,
            captured_stdout,
            captured_stderr,
            grace_seconds=PROCESS_CLEANUP_GRACE_SECONDS,
        )
        incomplete = tree_error is not None or not eof_proven
        status = (
            f"timeout-{APK_ANALYZER_TIMEOUT_SECONDS}s"
            if isinstance(error, subprocess.TimeoutExpired)
            else f"communicate-error={type(error).__name__}"
        )
        if tree_proof:
            status += f";{tree_proof}"
        if drain_error:
            status += f";{drain_error}"
        if incomplete:
            status += ";evidence_incomplete=1"
        retain_process_diagnostics(stdout, stderr, status)
        _set_exception_output(error, stdout, stderr)
        if tree_error is not None:
            raise tree_error from error
        if isinstance(error, subprocess.TimeoutExpired):
            timeout_error = subprocess.TimeoutExpired(
                command,
                APK_ANALYZER_TIMEOUT_SECONDS,
                output=output_text(stdout),
                stderr=output_text(stderr),
            )
            raise timeout_error from error
        raise
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
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise VerificationError(
            f"apkanalyzer timed out after {APK_ANALYZER_TIMEOUT_SECONDS} seconds",
        ) from error
    except OSError as error:
        stdout, stderr = _exception_output(error)
        emit_process_diagnostic(stdout, stderr)
        raise
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
