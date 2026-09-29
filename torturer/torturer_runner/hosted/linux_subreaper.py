"""Command-scoped Linux descendant containment for hosted qualification."""

from __future__ import annotations

import ctypes
import os
from pathlib import Path
import signal
import sys
import time


_PR_SET_CHILD_SUBREAPER = 36
_PR_GET_CHILD_SUBREAPER = 37
_SETUP_FAILURE = 125
_EXEC_FAILURE = 127
_BOUND_DESCENDANTS_ENV = "DOBBYVPN_SUBREAPER_BOUND_DESCENDANTS"
_BOUND_REAP_TIMEOUT_SECONDS = 1.0
_BOUND_REAP_POLL_SECONDS = 0.01


def _write_status(descriptor: int, status: bytes) -> bool:
    """Write one atomic status record to the runner-only control pipe."""

    try:
        os.write(descriptor, status + b"\n")
        return True
    except OSError:
        os.write(2, b"DOBBYVPN_SUBREAPER_STATUS_FAILED\n")
        return False


def _enable_subreaper() -> bool:
    try:
        libc = ctypes.CDLL(None, use_errno=True)
        prctl = libc.prctl
        prctl.argtypes = [
            ctypes.c_int,
            ctypes.c_ulong,
            ctypes.c_ulong,
            ctypes.c_ulong,
            ctypes.c_ulong,
        ]
        prctl.restype = ctypes.c_int
        if prctl(_PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != 0:
            return False
        enabled = ctypes.c_int(0)
        if (
            prctl(
                _PR_GET_CHILD_SUBREAPER,
                ctypes.addressof(enabled),
                0,
                0,
                0,
            )
            != 0
        ):
            return False
        return enabled.value == 1
    except (AttributeError, OSError, TypeError, ValueError):
        return False


def _mirror_leader_status(status: int) -> int:
    if os.WIFEXITED(status):
        return os.WEXITSTATUS(status)
    if os.WIFSIGNALED(status):
        signum = os.WTERMSIG(status)
        if signum not in (signal.SIGKILL, signal.SIGSTOP):
            signal.signal(signum, signal.SIG_DFL)
        os.kill(os.getpid(), signum)
    return _SETUP_FAILURE


def _direct_child_pids() -> tuple[int, ...] | None:
    """Read only this supervisor's direct children from procfs."""

    try:
        value = Path(
            f"/proc/{os.getpid()}/task/{os.getpid()}/children"
        ).read_text(encoding="ascii")
    except FileNotFoundError:
        return ()
    except (OSError, UnicodeError):
        return None
    result: list[int] = []
    for field in value.split():
        try:
            pid = int(field)
        except ValueError:
            return None
        if pid <= 0:
            return None
        result.append(pid)
    return tuple(result)


def _bound_remaining_descendants() -> tuple[bool, bool]:
    """Kill adopted descendants without waiting past the parent deadline.

    Returns ``(survivor_seen, containment_complete)``.  The supervisor owns
    every process returned by its ``children`` file, so signalling these PIDs
    cannot target an unrelated host process. A survivor marker is returned on
    the control pipe; an incomplete census remains a process-cleanup failure.
    """

    # Reap any already-exited child first.  Reaping a zombie is ordinary
    # cleanup, not evidence that a live descendant had to be killed.
    while True:
        try:
            pid, _status = os.waitpid(-1, os.WNOHANG)
        except ChildProcessError:
            return False, True
        except InterruptedError:
            continue
        if pid == 0:
            break
    children = _direct_child_pids()
    if children is None:
        return True, False
    if not children:
        return False, True

    # A live child remains owned by this supervisor. Kill only those exact
    # direct children.  A killed child may have grandchildren; subreaper
    # adoption makes those grandchildren direct children of this supervisor,
    # so recensus and kill them in the same bounded loop rather than waiting
    # indefinitely for a descendant that escaped the leader.
    survivor_seen = True
    reap_deadline = time.monotonic() + _BOUND_REAP_TIMEOUT_SECONDS
    while True:
        children = _direct_child_pids()
        if children is None:
            return survivor_seen, False
        if not children:
            return survivor_seen, True
        for pid in children:
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                continue
            except OSError:
                return survivor_seen, False
        while True:
            try:
                pid, _status = os.waitpid(-1, os.WNOHANG)
            except ChildProcessError:
                break
            except InterruptedError:
                continue
            if pid == 0:
                break
        if time.monotonic() >= reap_deadline:
            children = _direct_child_pids()
            return survivor_seen, children == ()
        time.sleep(_BOUND_REAP_POLL_SECONDS)


def main() -> int:
    if (
        os.name != "posix"
        or len(sys.argv) < 5
        or sys.argv[1] != "--status-fd"
        or sys.argv[3] != "--"
    ):
        return _SETUP_FAILURE
    try:
        status_descriptor = int(sys.argv[2])
        if status_descriptor < 0:
            raise ValueError
        # The forked candidate inherits this descriptor only until exec.
        # CLOEXEC prevents untrusted candidate code from writing trusted
        # helper state or keeping the control pipe open.
        os.set_inheritable(status_descriptor, False)
    except (OSError, ValueError):
        os.write(2, b"DOBBYVPN_SUBREAPER_SETUP_FAILED\n")
        return _SETUP_FAILURE
    if not _enable_subreaper():
        _write_status(status_descriptor, b"SETUP_FAILED")
        os.write(2, b"DOBBYVPN_SUBREAPER_SETUP_FAILED\n")
        return _SETUP_FAILURE
    if not _write_status(status_descriptor, b"READY"):
        return _SETUP_FAILURE

    command = sys.argv[4:]
    try:
        leader = os.fork()
    except OSError:
        _write_status(status_descriptor, b"FORK_FAILED")
        os.write(2, b"DOBBYVPN_SUBREAPER_FORK_FAILED\n")
        return _SETUP_FAILURE
    if leader == 0:
        try:
            command_environment = os.environ.copy()
            command_environment.pop(_BOUND_DESCENDANTS_ENV, None)
            os.execvpe(command[0], command, command_environment)
        except OSError:
            _write_status(status_descriptor, b"EXEC_FAILED")
            os.write(2, b"DOBBYVPN_SUBREAPER_EXEC_FAILED\n")
            os._exit(_EXEC_FAILURE)

    bound_descendants = os.environ.get(_BOUND_DESCENDANTS_ENV) == "1"
    leader_status: int | None = None
    while leader_status is None:
        try:
            _pid, leader_status = os.waitpid(leader, 0)
        except InterruptedError:
            continue
        except ChildProcessError:
            break
    if bound_descendants:
        survivor_seen, containment_complete = _bound_remaining_descendants()
        if survivor_seen:
            _write_status(status_descriptor, b"SURVIVOR_KILLED")
            os.write(2, b"DOBBYVPN_SUBREAPER_SURVIVOR_KILLED=1\n")
        if not containment_complete:
            _write_status(status_descriptor, b"SURVIVOR_UNCONTAINED")
            os.write(2, b"DOBBYVPN_SUBREAPER_SURVIVOR_UNCONTAINED=1\n")
    else:
        # Keep adopted descendants attributable until the command leader has
        # exited.  In particular, the outer hosted deadline must not reap the
        # intentionally restarted VPN service while the inner functional
        # process still needs its /proc identity for strict finalization.
        # Once the leader is gone, preserve the ordinary runner contract by
        # waiting for and reaping every remaining descendant before return.
        while True:
            try:
                os.wait()
            except InterruptedError:
                continue
            except ChildProcessError:
                break
    if leader_status is None:
        _write_status(status_descriptor, b"WAIT_FAILED")
        os.write(2, b"DOBBYVPN_SUBREAPER_WAIT_FAILED\n")
        return _SETUP_FAILURE
    if not _write_status(status_descriptor, b"COMPLETE"):
        return _SETUP_FAILURE
    return _mirror_leader_status(leader_status)


if __name__ == "__main__":
    raise SystemExit(main())
