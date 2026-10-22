#!/usr/bin/env python3
"""Capture Android emulator logs and ADB transport output during qualification."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import sys
import time
import traceback
from datetime import datetime, timezone


CHILD_RECOVERY_SECONDS = 3.0
STOP_WAIT_SECONDS = 5.0
_STOP_REQUESTED = False
_EVENT_WRITE_FAILED = False


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def process_identity(pid: int) -> str | None:
    """Identify a live Linux process without treating a zombie as running."""
    try:
        stat = Path(f"/proc/{pid}/stat").read_text(encoding="ascii")
        boot_id = Path("/proc/sys/kernel/random/boot_id").read_text(encoding="ascii").strip()
        fields = stat.rsplit(")", 1)[1].split()
        if fields[0] == "Z":
            return None
        return f"{boot_id}:{fields[19]}"
    except (OSError, IndexError):
        return None


def _read_json(path: Path) -> dict[str, object] | None:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


def _write_json(path: Path, value: object) -> None:
    temporary = path.with_suffix(path.suffix + ".partial")
    temporary.write_text(json.dumps(value, sort_keys=True) + "\n", encoding="utf-8")
    temporary.replace(path)


def _event(path: Path, event_name: str, **fields: object) -> bool:
    global _EVENT_WRITE_FAILED
    record = {"timestamp_utc": utc_now(), "event": event_name, **fields}
    try:
        with path.open("ab") as stream:
            stream.write((json.dumps(record, sort_keys=True) + "\n").encode("utf-8"))
            stream.flush()
        return True
    except BaseException:
        _EVENT_WRITE_FAILED = True
        traceback.print_exc(file=sys.stderr)
        return False


def _same_process(owner: dict[str, object]) -> bool:
    try:
        pid = int(owner["pid"])
    except (KeyError, TypeError, ValueError):
        return False
    identity = process_identity(pid)
    return identity is not None and identity == owner.get("process_identity")


def _owned_group(owner: dict[str, object]) -> bool:
    if not _same_process(owner):
        return False
    try:
        pid = int(owner["pid"])
        return int(owner["process_group"]) == pid == os.getpgid(pid)
    except (KeyError, TypeError, ValueError, OSError):
        return False


def _start_child(
    adb: str,
    name: str,
    arguments: list[str],
    diagnostics: Path,
    events: Path,
    deadline_epoch: float,
    *,
    pipe_stdout: bool,
) -> dict[str, object] | None:
    if time.time() >= deadline_epoch:
        _event(events, "child_not_started", name=name, reason="qualification deadline expired")
        return None

    stdout_path = diagnostics / f"{name}.stdout.bin" if pipe_stdout else diagnostics / f"{name}.stdout.log"
    stderr_path = diagnostics / f"{name}.stderr.log"
    stdout_file = None
    stderr_file = None
    process = None
    try:
        stderr_file = stderr_path.open("wb", buffering=0)
        stdout = subprocess.PIPE if pipe_stdout else (stdout_path.open("wb", buffering=0))
        if not pipe_stdout:
            stdout_file = stdout
        process = subprocess.Popen(
            [adb, *arguments],
            stdin=subprocess.DEVNULL,
            stdout=stdout,
            stderr=stderr_file,
            bufsize=0,
            env=os.environ.copy(),
        )
        child = {"name": name, "pid": process.pid, "process": process,
                 "stdout_path": str(stdout_path), "stderr_path": str(stderr_path),
                 "exit_kind": None, "returncode": None, "forced": False}
        _event(events, "child_started", name=name, command=[adb, *arguments], pid=process.pid)
        return child
    except BaseException as error:
        if process is not None and process.poll() is None:
            try:
                process.kill()
                process.wait(timeout=CHILD_RECOVERY_SECONDS)
            except BaseException:
                traceback.print_exc(file=sys.stderr)
        else:
            for path in (stdout_path, stderr_path):
                try:
                    if path.exists() and path.stat().st_size == 0:
                        path.unlink()
                except OSError:
                    traceback.print_exc(file=sys.stderr)
        _event(
            events,
            "child_start_failed",
            name=name,
            command=[adb, *arguments],
            error_type=type(error).__name__,
            error=str(error),
            traceback=traceback.format_exc(),
        )
        return None
    finally:
        if stdout_file is not None:
            stdout_file.close()
        if stderr_file is not None:
            stderr_file.close()


def _mark_exit(child: dict[str, object], events: Path, *, requested: bool, forced: bool = False) -> None:
    if child["exit_kind"] is not None:
        return
    process = child["process"]
    assert isinstance(process, subprocess.Popen)
    returncode = process.poll()
    if returncode is None:
        return
    kind = "forced_kill" if forced else ("requested_stop" if requested else "natural_exit")
    child.update(returncode=returncode, exit_kind=kind, forced=forced)
    _event(events, "child_exited", name=child["name"], pid=process.pid, returncode=returncode,
           exit_kind=kind, forced=forced)


def _drain_tracker(
    selector: selectors.BaseSelector,
    raw_output,
    events: Path,
    *,
    timeout: float,
) -> bool:
    try:
        if not selector.get_map():
            time.sleep(timeout)
            return True
        for key, _ in selector.select(timeout):
            chunk = os.read(key.fd, 65536)
            if not chunk:
                selector.unregister(key.fileobj)
                _event(events, "transport_stream_closed")
                continue
            offset = raw_output.tell()
            written = raw_output.write(chunk)
            if written != len(chunk):
                raise OSError(f"short write retaining ADB transport bytes: {written} of {len(chunk)}")
            _event(events, "transport_bytes", raw_offset=offset, byte_count=len(chunk))
        return True
    except BaseException:
        traceback.print_exc(file=sys.stderr)
        try:
            for key in list(selector.get_map().values()):
                selector.unregister(key.fileobj)
        except (KeyError, OSError, ValueError):
            traceback.print_exc(file=sys.stderr)
        return False


def _finish_children(
    children: list[dict[str, object]], selector: selectors.BaseSelector, raw_output, events: Path,
) -> tuple[bool, bool]:
    """Stop all children within one shared recovery budget; return unresolved/error state."""
    recovery_deadline = time.monotonic() + CHILD_RECOVERY_SECONDS
    terminate_deadline = time.monotonic() + CHILD_RECOVERY_SECONDS / 2
    requested: set[str] = set()
    forced: set[str] = set()
    drain_error = False
    for child in children:
        process = child["process"]
        assert isinstance(process, subprocess.Popen)
        _mark_exit(child, events, requested=False)
        if child["exit_kind"] is None:
            requested.add(str(child["name"]))
            _event(events, "child_stop_requested", name=child["name"], pid=process.pid)
            try:
                process.terminate()
            except ProcessLookupError:
                pass
            except OSError:
                traceback.print_exc(file=sys.stderr)

    while time.monotonic() < terminate_deadline:
        for child in children:
            _mark_exit(child, events, requested=str(child["name"]) in requested)
        if all(child["exit_kind"] is not None for child in children):
            break
        if not _drain_tracker(selector, raw_output, events, timeout=min(0.05, max(0, terminate_deadline-time.monotonic()))):
            drain_error = True

    for child in children:
        process = child["process"]
        assert isinstance(process, subprocess.Popen)
        if child["exit_kind"] is None:
            forced.add(str(child["name"]))
            try:
                process.kill()
                _event(events, "child_kill_requested", name=child["name"], pid=process.pid, forced=True)
            except ProcessLookupError:
                pass
            except OSError:
                traceback.print_exc(file=sys.stderr)

    while time.monotonic() < recovery_deadline:
        for child in children:
            _mark_exit(child, events, requested=str(child["name"]) in requested,
                       forced=str(child["name"]) in forced)
        if all(child["exit_kind"] is not None for child in children):
            break
        if not _drain_tracker(selector, raw_output, events, timeout=min(0.05, max(0, recovery_deadline-time.monotonic()))):
            drain_error = True

    unresolved = False
    for child in children:
        process = child["process"]
        assert isinstance(process, subprocess.Popen)
        _mark_exit(child, events, requested=str(child["name"]) in requested,
                   forced=str(child["name"]) in forced)
        if child["exit_kind"] is None:
            unresolved = True
            child["exit_kind"] = "forced_unresolved" if child["name"] in forced else "stop_unresolved"
            child["forced"] = child["name"] in forced
            _event(events, "child_stop_unresolved", name=child["name"], pid=process.pid,
                   forced=child["forced"])
    # A child can exit with unread bytes still in its pipe. Retain them through
    # EOF inside the same recovery allowance before closing the read endpoint.
    while selector.get_map() and time.monotonic() < recovery_deadline:
        if not _drain_tracker(selector, raw_output, events,
                              timeout=min(0.05, max(0, recovery_deadline-time.monotonic()))):
            drain_error = True
            break
    if selector.get_map():
        drain_error = True
        _event(events, "transport_collection_incomplete", reason="shared recovery deadline expired before EOF")
    return unresolved or bool(forced), drain_error


def _capture(adb: str, run_dir: Path, deadline_epoch: float) -> int:
    global _EVENT_WRITE_FAILED
    _EVENT_WRITE_FAILED = False
    diagnostics = run_dir / "android-live"
    diagnostics.mkdir(parents=True, exist_ok=True)
    events = diagnostics / "events.jsonl"
    raw_path = diagnostics / "adb-track-devices.stdout.bin"
    children: list[dict[str, object]] = []
    errors: list[str] = []
    reason = "qualification-deadline"
    if not _event(events, "capture_started", supervisor_pid=os.getpid(), deadline_epoch=deadline_epoch):
        errors.append("could not write capture start event; see supervisor stderr")

    if time.time() >= deadline_epoch:
        errors.append("qualification deadline had expired before ADB capture startup")
        _event(events, "capture_not_started", reason=errors[-1])
        _write_json(diagnostics / "result.json", {
            "timestamp_utc": utc_now(), "status": "diagnostic_error", "reason": "deadline_expired_before_start",
            "children": [], "errors": errors,
        })
        return 1

    tracker_environment = os.environ.copy()
    trace_flags = [value for value in tracker_environment.get("ADB_TRACE", "").split(",") if value]
    if "transport" not in trace_flags:
        trace_flags.append("transport")
    os.environ["ADB_TRACE"] = ",".join(trace_flags)

    selector = selectors.DefaultSelector()
    raw_output = None
    try:
        logcat = _start_child(adb, "android-logcat", ["logcat", "-b", "all", "-v", "epoch"],
                              diagnostics, events, deadline_epoch, pipe_stdout=False)
        if logcat is not None:
            children.append(logcat)
        else:
            errors.append("Logcat could not be started; see child_start_failed or child_not_started event")
        tracker = _start_child(adb, "adb-track-devices", ["track-devices", "-l"],
                               diagnostics, events, deadline_epoch, pipe_stdout=True)
        if tracker is not None:
            children.append(tracker)
            process = tracker["process"]
            assert isinstance(process, subprocess.Popen) and process.stdout is not None
            raw_output = raw_path.open("wb", buffering=0)
            selector.register(process.stdout, selectors.EVENT_READ)
        else:
            errors.append("ADB transport tracking could not be started; see child_start_failed or child_not_started event")

        while True:
            if not children:
                reason = "no_diagnostic_children_started"
                errors.append("neither ADB diagnostic process could be started")
                break
            if _STOP_REQUESTED:
                reason = "workflow-cleanup"
                break
            remaining = deadline_epoch - time.time()
            if remaining <= 0:
                reason = "qualification-deadline"
                break
            for child in children:
                before = child["exit_kind"]
                _mark_exit(child, events, requested=False)
                if before is None and child["exit_kind"] == "natural_exit":
                    errors.append(f"{child['name']} exited before diagnostic shutdown")
            if children and all(child["exit_kind"] is not None for child in children):
                reason = "all_diagnostic_children_exited"
                break
            if not _drain_tracker(selector, raw_output, events, timeout=min(0.1, remaining)):
                errors.append("could not retain ADB transport stream; see supervisor stderr")
                reason = "transport_output_error"
                break
    except BaseException as error:
        errors.append(f"capture error: {type(error).__name__}: {error}")
        _event(events, "capture_error", error_type=type(error).__name__, error=str(error),
               traceback=traceback.format_exc())
    finally:
        forced_or_unresolved, drain_error = _finish_children(children, selector, raw_output, events)
        if forced_or_unresolved:
            errors.append("one or more ADB diagnostic children required forced termination or remained unresolved")
        if drain_error:
            errors.append("could not retain the final ADB transport bytes; see supervisor stderr")
        for key in list(selector.get_map().values()):
            try:
                selector.unregister(key.fileobj)
            except (KeyError, OSError, ValueError):
                pass
        selector.close()
        for child in children:
            process = child["process"]
            if isinstance(process, subprocess.Popen) and process.stdout is not None:
                process.stdout.close()
            if child["exit_kind"] in {"natural_exit", "forced_kill", "forced_unresolved", "stop_unresolved"}:
                if child["exit_kind"] != "requested_stop":
                    errors.append(f"{child['name']} ended as {child['exit_kind']}")
        if raw_output is not None:
            raw_output.close()
        status = "diagnostic_error" if errors or _EVENT_WRITE_FAILED else "captured"
        _event(events, "capture_finished", reason=reason, status=status)
        if _EVENT_WRITE_FAILED:
            errors.append("could not write one or more diagnostic events; see supervisor stderr")
        status = "diagnostic_error" if errors else "captured"
        try:
            _write_json(diagnostics / "result.json", {
                "timestamp_utc": utc_now(), "status": status, "reason": reason,
                "deadline_epoch": deadline_epoch,
                "children": [{key: value for key, value in child.items() if key != "process"}
                             for child in children],
                "errors": errors,
            })
        except BaseException:
            traceback.print_exc(file=sys.stderr)
    return 1 if errors else 0


def _start(args: argparse.Namespace) -> int:
    run_dir = Path(args.run_dir).resolve()
    diagnostics = run_dir / "android-live"
    diagnostics.mkdir(parents=True, exist_ok=True)
    owner_path = diagnostics / "owner.json"
    result_path = diagnostics / "result.json"
    if time.time() >= args.deadline_epoch:
        _write_json(result_path, {"timestamp_utc": utc_now(), "status": "diagnostic_error",
                                  "reason": "deadline_expired_before_start", "children": [],
                                  "errors": ["qualification deadline had expired before diagnostic startup"]})
        print("Android diagnostics not started: qualification deadline already expired", file=sys.stderr)
        return 1
    existing = _read_json(owner_path)
    if existing is not None and _same_process(existing):
        print(f"Android diagnostics already running: pid={existing.get('pid')}", file=sys.stderr)
        return 1

    script = Path(__file__).resolve()
    stdout_path = diagnostics / "supervisor.stdout.log"
    stderr_path = diagnostics / "supervisor.stderr.log"
    try:
        with stdout_path.open("wb", buffering=0) as stdout, stderr_path.open("wb", buffering=0) as stderr:
            process = subprocess.Popen(
                [sys.executable, str(script), "capture", "--adb", args.adb,
                 "--run-dir", str(run_dir), "--deadline-epoch", str(args.deadline_epoch)],
                stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr, start_new_session=True,
            )
    except BaseException as error:
        for path in (stdout_path, stderr_path):
            try:
                if path.exists() and path.stat().st_size == 0:
                    path.unlink()
            except OSError:
                traceback.print_exc(file=sys.stderr)
        _write_json(result_path, {"timestamp_utc": utc_now(), "status": "diagnostic_error",
                                  "reason": "supervisor_start_failed", "children": [],
                                  "errors": [f"{type(error).__name__}: {error}", traceback.format_exc()]})
        traceback.print_exc(file=sys.stderr)
        return 1
    owner = {"pid": process.pid, "process_group": process.pid,
             "process_identity": process_identity(process.pid), "started_at_utc": utc_now(),
             "deadline_epoch": args.deadline_epoch}
    try:
        _write_json(owner_path, owner)
    except BaseException as error:
        original_error = traceback.format_exc()
        traceback.print_exc(file=sys.stderr)
        group_kill_requested = False
        try:
            os.killpg(process.pid, signal.SIGKILL)
            group_kill_requested = True
        except ProcessLookupError:
            pass
        except OSError:
            traceback.print_exc(file=sys.stderr)
        try:
            process.wait(timeout=CHILD_RECOVERY_SECONDS)
        except BaseException:
            traceback.print_exc(file=sys.stderr)
        result = {
            "timestamp_utc": utc_now(), "status": "diagnostic_error",
            "reason": "owner_record_write_failed", "supervisor_pid": process.pid,
            "group_kill_requested": group_kill_requested,
            "supervisor_exit_confirmed": process.poll() is not None,
            "supervisor_returncode": process.returncode,
            "children_cleanup": (
                "group termination requested; individual child exits unconfirmed" if group_kill_requested
                else "group termination not performed; individual child exits unconfirmed"
            ),
            "errors": [f"{type(error).__name__}: {error}", original_error],
        }
        # Stderr remains available even when the failed filesystem cannot retain
        # the result. Waiting here reaps this launcher's own supervisor child.
        print(json.dumps(result, sort_keys=True), file=sys.stderr)
        try:
            _write_json(result_path, result)
        except BaseException:
            traceback.print_exc(file=sys.stderr)
        return 1
    print(f"Android live diagnostics started: pid={process.pid}; owner={owner_path}")
    return 0


def _stop(args: argparse.Namespace) -> int:
    diagnostics = Path(args.run_dir).resolve() / "android-live"
    owner = _read_json(diagnostics / "owner.json")
    result_path = diagnostics / "result.json"
    if owner is None:
        result = _read_json(result_path)
        print(f"Android diagnostics have no owner record; result={json.dumps(result, sort_keys=True) if result else 'unavailable'}",
              file=sys.stderr)
        return 1
    if not _same_process(owner):
        result = _read_json(result_path)
        if result is not None:
            print(f"Android diagnostics already finished: status={result.get('status')}")
            return 0 if result.get("status") == "captured" else 1
        result = {
            "timestamp_utc": utc_now(), "status": "diagnostic_error",
            "reason": "supervisor_exited_without_result", "children_cleanup": "unconfirmed",
            "errors": ["supervisor identity is no longer live; child ownership and cleanup cannot be confirmed"],
        }
        _write_json(result_path, result)
        print(f"Android diagnostics owner is no longer live and has no result: pid={owner.get('pid')}; "
              "child ownership and cleanup remain unconfirmed", file=sys.stderr)
        return 1

    pid = int(owner["pid"])
    requested_at = utc_now()
    try:
        os.kill(pid, signal.SIGTERM)
    except OSError:
        traceback.print_exc(file=sys.stderr)
    wait_until = time.monotonic() + STOP_WAIT_SECONDS
    while time.monotonic() < wait_until:
        result = _read_json(result_path)
        if result is not None and not _same_process(owner):
            print(f"Android diagnostics stopped: status={result.get('status')}; reason={result.get('reason')}")
            return 0 if result.get("status") == "captured" else 1
        time.sleep(0.05)

    fallback_attempted = False
    if _owned_group(owner):
        fallback_attempted = True
        try:
            os.killpg(pid, signal.SIGTERM)
        except OSError:
            traceback.print_exc(file=sys.stderr)
        grace_until = time.monotonic() + 0.5
        while time.monotonic() < grace_until and _same_process(owner):
            time.sleep(0.05)
        if _owned_group(owner):
            try:
                os.killpg(pid, signal.SIGKILL)
            except OSError:
                traceback.print_exc(file=sys.stderr)
    result = _read_json(result_path)
    if result is None:
        result = {"timestamp_utc": utc_now(), "status": "forced_or_unresolved",
                  "reason": "supervisor did not finish within the bounded stop wait",
                  "supervisor_exit_confirmed": not _same_process(owner),
                  "group_fallback_attempted": fallback_attempted,
                  "requested_at_utc": requested_at,
                  "errors": ["capture result was unavailable after bounded shutdown"]}
        try:
            _write_json(result_path, result)
        except BaseException:
            traceback.print_exc(file=sys.stderr)
    print(f"Android diagnostics did not stop cleanly: {json.dumps(result, sort_keys=True)}", file=sys.stderr)
    return 1


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    start = commands.add_parser("start", help="start bounded live Android diagnostics")
    start.add_argument("--adb", required=True)
    start.add_argument("--run-dir", required=True)
    start.add_argument("--deadline-epoch", type=float, required=True)
    stop = commands.add_parser("stop", help="stop this run's owned diagnostic supervisor")
    stop.add_argument("--run-dir", required=True)
    capture = commands.add_parser("capture", help=argparse.SUPPRESS)
    capture.add_argument("--adb", required=True)
    capture.add_argument("--run-dir", required=True)
    capture.add_argument("--deadline-epoch", type=float, required=True)
    args = parser.parse_args(argv)
    if args.command == "start":
        return _start(args)
    if args.command == "stop":
        return _stop(args)

    def request_stop(signum: int, frame: object) -> None:
        global _STOP_REQUESTED
        _STOP_REQUESTED = True

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    return _capture(args.adb, Path(args.run_dir).resolve(), args.deadline_epoch)


if __name__ == "__main__":
    raise SystemExit(main())
