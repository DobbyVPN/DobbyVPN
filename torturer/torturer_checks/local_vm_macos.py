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
import plistlib
import re
import subprocess
import sys
from typing import Any

from .local_vm import LocalVMError


class MacOSInteractiveDesktopUnavailable(LocalVMError):
    """The current process cannot launch a visible Aqua window."""

    reason_code = "MACOS_AQUA_SESSION_UNAVAILABLE"

    def __init__(self, detail: str) -> None:
        super().__init__(f"{self.reason_code}: {detail}")


_MACOS_ACCESSIBILITY_PROBE = '''tell application "System Events"
    if not (exists process "Finder") then error "Finder is unavailable"
    if (visible of process "Finder") is false then error "Finder is not visible"
    return "Finder"
end tell'''

# Keep the final macOS command boundary defensive even when called directly by
# a test or another local worker.  PATH is needed for the smoke driver's
# ``osascript``, ``pgrep`` and clipboard helpers; HOME is needed by the Go
# desktop UI/CLI user stores.  The socket is the one product runtime value
# needed by the macOS client.  Developer/build loader variables are not needed
# by the packaged app and are intentionally not forwarded.
_MACOS_NATIVE_UI_ENVIRONMENT = frozenset({
    "PATH",
    "HOME",
    "DOBBYVPN_CONTROL_SOCKET",
})


def _validate_native_ui_bundle(bundle: Path) -> Path:
    executable = bundle / "Contents" / "MacOS" / "DobbyVPNMacApp"
    info = bundle / "Contents" / "Info.plist"
    if not executable.is_file() or not info.is_file():
        raise LocalVMError("macOS native UI bundle is incomplete")
    try:
        with info.open("rb") as stream:
            metadata = plistlib.load(stream)
    except (OSError, plistlib.InvalidFileException, ValueError) as error:
        raise LocalVMError("macOS native UI bundle metadata is invalid") from error
    if not isinstance(metadata, dict) or metadata.get("CFBundleExecutable") != "DobbyVPNMacApp":
        raise LocalVMError("macOS native UI bundle executable metadata is invalid")
    return bundle.resolve()


def _filtered_native_ui_environment(environment: dict[str, str]) -> dict[str, str]:
    """Return only values explicitly required by the macOS native journey."""

    return {
        name: environment[name]
        for name in _MACOS_NATIVE_UI_ENVIRONMENT
        if isinstance(environment.get(name), str)
    }


def stage_native_ui_bundle(executable: str | Path) -> Path:
    """Validate and return the native app bundle built for this candidate."""

    return _validate_native_ui_bundle(Path(executable))


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
    input_data: bytes | None = None,
) -> subprocess.CompletedProcess[bytes]:
    """Run one short platform probe through the logged command runner."""

    try:
        kwargs: dict[str, Any] = {
            "cwd": run_dir,
            "logs": logs,
            "label": label,
            "timeout": min(timeout, 5.0),
            "check": False,
        }
        if input_data is not None:
            kwargs["input_data"] = input_data
        return _run_logged(command, **kwargs)
    except Exception as error:
        raise MacOSInteractiveDesktopUnavailable(
            "Aqua desktop preflight could not complete"
        ) from error


def preflight_native_ui_capabilities(
    smoke_script: Path,
    *,
    run_dir: Path,
    logs: Path,
    timeout: float,
) -> None:
    """Invoke the single-owned product-independent native capability gate."""

    if not smoke_script.is_file() or smoke_script.is_symlink():
        raise MacOSInteractiveDesktopUnavailable(
            "native UI capability preflight script is unavailable"
        )
    try:
        result = _run_logged(
            [
                sys.executable,
                str(smoke_script),
                "--platform", "macos",
                "--preflight-only",
                "--timeout", str(max(1.0, min(timeout, 30.0))),
            ],
            cwd=smoke_script.parents[2],
            logs=logs,
            label="macos-native-capability-preflight",
            timeout=min(timeout, 30.0),
            check=False,
        )
    except Exception as error:
        raise MacOSInteractiveDesktopUnavailable(
            "macOS native capability preflight could not complete"
        ) from error
    if result.returncode != 0:
        raise MacOSInteractiveDesktopUnavailable(
            f"macOS native capability preflight exited with status {result.returncode}"
        )


def _parse_console_user_state(stdout: bytes) -> tuple[str, int] | None:
    """Extract the ConsoleUser name and UID from logged platform state."""

    user: str | None = None
    uid: int | None = None
    for line in stdout.decode("utf-8", errors="replace").splitlines():
        if ":" not in line:
            continue
        key, value = (part.strip() for part in line.split(":", 1))
        if key == "kCGSSessionUserNameKey":
            if user is not None or re.fullmatch(r"[^\s:{}]+", value) is None:
                return None
            user = value
        elif key == "kCGSSessionUserIDKey":
            if uid is not None or re.fullmatch(r"[1-9][0-9]*", value) is None:
                return None
            try:
                uid = int(value)
            except ValueError:
                return None
    if user is None or uid is None:
        return None
    return user, uid


def _screen_values(stdout: bytes) -> tuple[str, ...] | None:
    """Parse WindowServer's lock values, rejecting a malformed key."""

    text = stdout.decode("utf-8", errors="replace")
    if not text.strip():
        return None
    matches = re.findall(
        r'"CGSSessionScreenIsLocked"\s*=\s*([^\s,;}]+)',
        text,
    )
    if "CGSSessionScreenIsLocked" in text and not matches:
        return None
    if any(value not in {"Yes", "No"} for value in matches):
        return None
    if not matches and "Root" not in text:
        return None
    return tuple(matches)


def _screen_is_unlocked(stdout: bytes, *, finder_accessible: bool = False) -> bool:
    """Treat explicit state as authoritative; infer unlocked only with Finder evidence."""

    values = _screen_values(stdout)
    if values is None:
        return False
    if values:
        return set(values) == {"No"}
    return finder_accessible


def _screen_is_explicitly_locked(stdout: bytes) -> bool:
    values = _screen_values(stdout)
    return bool(values) and set(values) == {"Yes"}


def _finder_accessibility_available(result: subprocess.CompletedProcess[bytes]) -> bool:
    output = (
        result.stdout.decode("utf-8", errors="replace")
        if isinstance(result.stdout, bytes)
        else str(result.stdout)
    )
    return result.returncode == 0 and output.strip() == "Finder"


def preflight_interactive_desktop(
    *, run_dir: Path, logs: Path, timeout: float,
) -> tuple[str, int]:
    """Prove a usable Aqua session before launching the production window.

    SystemConfiguration's ConsoleUser state must identify the SSH worker, that
    user's GUI launchd domain must be present, WindowServer must report a
    non-conflicting screen state, and System Events must be able to inspect
    Finder. All checks are bounded and happen before the UI command is spawned;
    the caller then launches the command through that user's ``launchctl
    asuser`` bootstrap. An SSH/root-only, locked, or inaccessible guest is
    therefore an explicit unavailable full lane rather than a generic process
    failure.
    """

    if host_platform.system() != "Darwin":
        raise MacOSInteractiveDesktopUnavailable("macOS host is required")
    if timeout <= 0:
        raise MacOSInteractiveDesktopUnavailable("Aqua desktop preflight timeout is invalid")

    console = _probe(
        ["scutil"],
        run_dir=run_dir,
        logs=logs,
        label="macos-ui-console",
        timeout=timeout,
        input_data=b"show State:/Users/ConsoleUser\nquit\n",
    )
    parsed_console = (
        _parse_console_user_state(console.stdout)
        if console.returncode == 0
        else None
    )
    if parsed_console is None:
        raise MacOSInteractiveDesktopUnavailable("no logged-in Aqua console user")
    console_user, console_uid = parsed_console
    if console_user.lower() in {"root", "loginwindow"} or console_uid <= 0:
        raise MacOSInteractiveDesktopUnavailable("no logged-in Aqua console user")

    worker_user = getpass.getuser()
    worker_uid = os.getuid()
    if worker_user != console_user or worker_uid != console_uid or worker_uid <= 0:
        raise MacOSInteractiveDesktopUnavailable(
            "current process does not own the Aqua console session"
        )

    uid = str(console_uid)
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

    screen = _probe(
        ["ioreg", "-l", "-n", "Root", "-d", "1", "-w", "0"],
        run_dir=run_dir,
        logs=logs,
        label="macos-ui-screen",
        timeout=timeout,
    )
    accessibility = _probe(
        ["osascript", "-e", _MACOS_ACCESSIBILITY_PROBE],
        run_dir=run_dir,
        logs=logs,
        label="macos-ui-accessibility",
        timeout=timeout,
    )
    if not _finder_accessibility_available(accessibility):
        raise MacOSInteractiveDesktopUnavailable(
            "System Events accessibility permission is unavailable"
        )
    if screen.returncode != 0:
        raise MacOSInteractiveDesktopUnavailable("Aqua console screen state is unavailable")
    if _screen_is_explicitly_locked(screen.stdout):
        raise MacOSInteractiveDesktopUnavailable("Aqua console screen is locked")
    if not _screen_is_unlocked(screen.stdout, finder_accessible=True):
        raise MacOSInteractiveDesktopUnavailable("Aqua console screen state is unavailable")
    return console_user, console_uid


def run_interactive_ui(
    command: list[str],
    *,
    run_dir: Path,
    cwd: Path,
    logs: Path,
    timeout: float,
    environment: dict[str, str],
) -> subprocess.CompletedProcess[bytes]:
    """Preflight Aqua, then run the native UI in the user's GUI bootstrap."""

    console_user, console_uid = preflight_interactive_desktop(
        run_dir=run_dir, logs=logs, timeout=timeout
    )
    if "--smoke-script" in command:
        try:
            smoke_index = command.index("--smoke-script")
            smoke_script = Path(command[smoke_index + 1])
        except (ValueError, IndexError):
            raise MacOSInteractiveDesktopUnavailable(
                "native UI capability preflight script argument is missing"
            )
        preflight_native_ui_capabilities(
            smoke_script,
            run_dir=run_dir,
            logs=logs,
            timeout=timeout,
        )
    filtered_environment = _filtered_native_ui_environment(environment)
    asuser_command = [
        "sudo", "-n", "launchctl", "asuser", str(console_uid),
        "sudo", "-n", "-u", console_user, "--", "/usr/bin/env",
        *(
            f"{key}={value}"
            for key, value in sorted(filtered_environment.items())
        ),
        *command,
    ]
    return _run_logged(
        asuser_command,
        cwd=cwd,
        logs=logs,
        label="native-ui",
        timeout=timeout,
        environment=filtered_environment,
        check=False,
    )
