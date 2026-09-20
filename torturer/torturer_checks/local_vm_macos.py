"""Native macOS candidate lifecycle used by :mod:`local_vm`.

The local VM worker normally reaches a macOS guest over SSH.  That session is
not necessarily attached to the logged-in Aqua desktop, so a full native UI
lane must prove the desktop context before starting the production UI.  This
module owns only that bounded preflight and the subsequent command boundary;
the macOS service and functional adapter remain owned by ``local_vm``.
"""

from __future__ import annotations

import getpass
import os
from pathlib import Path
import platform as host_platform
import re
import subprocess
from typing import Any

from .local_vm import LocalVMError


class MacOSInteractiveDesktopUnavailable(LocalVMError):
    """The current process cannot launch a visible Aqua window."""

    reason_code = "MACOS_AQUA_SESSION_UNAVAILABLE"

    def __init__(self, detail: str) -> None:
        super().__init__(f"{self.reason_code}: {detail}")


def _run_logged(*args: Any, **kwargs: Any) -> subprocess.CompletedProcess[bytes]:
    """Delegate command logging to the common local-VM command runner."""

    from .local_vm import _run_logged as run_logged

    return run_logged(*args, **kwargs)


def _probe(
    command: list[str],
    *,
    run_dir: Path,
    logs: Path,
    label: str,
    timeout: float,
) -> subprocess.CompletedProcess[bytes]:
    """Run one short, logged platform probe without exposing its output."""

    try:
        return _run_logged(
            command,
            cwd=run_dir,
            logs=logs,
            label=label,
            timeout=min(timeout, 5.0),
            check=False,
        )
    except Exception as error:
        raise MacOSInteractiveDesktopUnavailable(
            "Aqua desktop preflight could not complete"
        ) from error


def preflight_interactive_desktop(
    *, run_dir: Path, logs: Path, timeout: float,
) -> None:
    """Prove a usable Aqua session before launching the production window.

    The SSH worker must be the console user, and that user's GUI launchd
    domain must be present.  Both checks are bounded and happen before the UI
    command is spawned, so an SSH/root-only guest is an explicit unavailable
    full lane rather than a generic process failure.
    """

    if host_platform.system() != "Darwin":
        raise MacOSInteractiveDesktopUnavailable("macOS host is required")
    if timeout <= 0:
        raise MacOSInteractiveDesktopUnavailable("Aqua desktop preflight timeout is invalid")

    console = _probe(
        ["stat", "-f", "%Su", "/dev/console"],
        run_dir=run_dir,
        logs=logs,
        label="macos-ui-console",
        timeout=timeout,
    )
    console_user = console.stdout.decode("utf-8", errors="replace").strip()
    if console.returncode != 0 or not console_user or console_user in {"root", "loginwindow"}:
        raise MacOSInteractiveDesktopUnavailable("no logged-in Aqua console user")
    if getpass.getuser() != console_user:
        raise MacOSInteractiveDesktopUnavailable(
            "current process does not own the Aqua console session"
        )

    uid = str(os.getuid())
    if re.fullmatch(r"[0-9]+", uid) is None:
        raise MacOSInteractiveDesktopUnavailable("current user ID is invalid")
    session = _probe(
        ["launchctl", "print", f"gui/{uid}"],
        run_dir=run_dir,
        logs=logs,
        label="macos-ui-session",
        timeout=timeout,
    )
    if session.returncode != 0:
        raise MacOSInteractiveDesktopUnavailable("Aqua launchd GUI session is unavailable")


def run_interactive_ui(
    command: list[str],
    *,
    run_dir: Path,
    cwd: Path,
    logs: Path,
    timeout: float,
    environment: dict[str, str],
) -> subprocess.CompletedProcess[bytes]:
    """Preflight Aqua, then pass through to the native UI command."""

    preflight_interactive_desktop(run_dir=run_dir, logs=logs, timeout=timeout)
    return _run_logged(
        command,
        cwd=cwd,
        logs=logs,
        label="native-ui",
        timeout=timeout,
        environment=environment,
        check=False,
    )
