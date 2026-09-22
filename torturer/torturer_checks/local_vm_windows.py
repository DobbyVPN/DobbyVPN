"""Native Windows candidate lifecycle used by :mod:`local_vm`.

The SSH session itself is the privileged SYSTEM boundary on Windows, so the
initial candidate service is an ordinary child process of that boundary.  A
full native journey may restart the exact service under the configured
interactive account; cleanup records that bounded owner allowance together
with the PID, creation ticks, and executable path before each side effect.
"""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import time
from typing import Any
import uuid

from .diagnostics import emit_streams
from .local_vm import LocalVMError

_PID = re.compile(r"^[1-9][0-9]*$")
_IDENTITY = re.compile(r"^[1-9][0-9]*\|[1-9][0-9]+$")
_INTERFACE = re.compile(r"^[1-9][0-9]*$")
_NATIVE_UI_CREATION_TICK_TOLERANCE = 10  # one microsecond in 100-ns ticks
_CONTROL_ADDRESS = "127.0.0.1:50051"
_FIREWALL_RULE = "DobbyVPN-Torturer-Routing-Probe"
_MESA_LLVMPIPE_URL = (
    "https://github.com/pal1000/mesa-dist-win/releases/download/26.2.0/"
    "mesa3d-26.2.0-release-msvc.7z"
)
_MESA_LLVMPIPE_SHA256 = "dcb2719ef346dab5b609fcb193a5f13cfc4b0502e3f4de1ad43d349477402f47"
_MESA_LLVMPIPE_MEMBERS = (
    "x64/opengl32.dll",
    "x64/libgallium_wgl.dll",
)
_MESA_LLVMPIPE_STAGING_NAME = "native-ui-staging"
_MESA_LLVMPIPE_ARCHIVE_NAME = "mesa3d-26.2.0-release-msvc.7z"
_NATIVE_UI_ENVIRONMENT = frozenset({
    "PROGRAMDATA",
    "DOBBYVPN_CONTROL_ADDRESS",
    "DOBBYVPN_CONTROL_TOKEN_USER",
    "DOBBY_LOG_PATH",
    "DOBBY_LOG_ROOT",
    "DOBBY_LOG_PRECREATED",
    "GODEBUG",
    # The Windows full lane's disposable Mesa fixture is scoped to the
    # interactive controller and the production UI child.  It must never be
    # copied into service/runtime or machine environment state.
    "GALLIUM_DRIVER",
    # native_ui_smoke.py resolves PowerShell through shutil.which() for
    # clipboard and UI Automation operations.  The scheduled task runs with
    # the interactive account's environment, but ProcessStartInfo receives a
    # deliberately bounded environment below, so carry the system PATH
    # explicitly rather than relying on .NET's inherited value.
    "PATH",
})
_NATIVE_UI_USER_ENVIRONMENT = (
    # Go's desktop source store uses USERPROFILE.  The other values are the
    # standard directories/runtime variables needed by Python, Fyne and
    # Windows UI Automation; none carries runner credentials.
    "APPDATA",
    "COMSPEC",
    "HOMEDRIVE",
    "HOMEPATH",
    "LOCALAPPDATA",
    "PATHEXT",
    "TEMP",
    "TMP",
    "USERPROFILE",
    "WINDIR",
    "SystemRoot",
)


def _error(message: str) -> Exception:
    # Keep this module importable while local_vm imports the platform modules.
    from .local_vm import LocalVMError

    return LocalVMError(message)


class WindowsInteractiveDesktopUnavailable(LocalVMError):
    """The configured user has no usable Explorer desktop session."""

    reason_code = "WINDOWS_INTERACTIVE_DESKTOP_UNAVAILABLE"

    def __init__(self, detail: str) -> None:
        super().__init__(f"{self.reason_code}: {detail}")


def _run_logged(*args: Any, **kwargs: Any) -> subprocess.CompletedProcess[bytes]:
    """Delegate logging/validation to the common local-VM command runner."""

    from .local_vm import _run_logged as run_logged

    return run_logged(*args, **kwargs)


def _save_state(run_dir: Path, runtime: dict[str, Any], status: str = "starting") -> None:
    from .local_vm import _read_state, _write_json

    state = _read_state(run_dir)
    if state is None:
        state = {"platform": "windows"}
    state["runtime"] = runtime
    state["status"] = status
    _write_json(run_dir / "platform.json", state)


def _service_path(descriptor: dict[str, Any]) -> Path:
    value = descriptor.get("service")
    if not isinstance(value, str):
        raise _error("Windows candidate service path is missing")
    path = Path(value).resolve()
    if not path.is_file():
        raise _error("Windows candidate service binary is missing")
    return path


def _powershell(script: str, *, cwd: Path, logs: Path, label: str, timeout: float,
                environment: dict[str, str] | None = None,
                check: bool = True) -> subprocess.CompletedProcess[bytes]:
    return _run_logged(
        ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script],
        cwd=cwd,
        logs=logs,
        label=label,
        timeout=timeout,
        environment=environment,
        check=check,
    )


def _powershell_literal(value: str) -> str:
    """Return a single-quoted PowerShell literal for a trusted path/value."""

    if "\x00" in value or "\r" in value or "\n" in value:
        raise _error("Windows UI task value contains a control character")
    return "'" + value.replace("'", "''") + "'"


def _mesa_llvmpipe_staging_path(run_dir: Path) -> Path:
    """Return the one disposable directory used by the Windows full lane."""

    root = run_dir.resolve()
    staging = root / _MESA_LLVMPIPE_STAGING_NAME
    try:
        staging.relative_to(root)
    except ValueError as error:  # pragma: no cover - fixed child path
        raise _error("Windows Mesa staging path escaped the run directory") from error
    return staging


def _remove_mesa_llvmpipe_staging(run_dir: Path) -> None:
    """Remove only the fixed disposable Mesa staging path."""

    staging = _mesa_llvmpipe_staging_path(run_dir)
    try:
        if staging.is_symlink() or staging.is_file():
            staging.unlink()
        elif staging.exists():
            shutil.rmtree(staging)
    except OSError as error:
        raise _error("Windows Mesa staging cleanup failed") from error


def _run_mesa_fixture_command(
    command: list[str], *, cwd: Path, timeout: float, logs: Path | None = None,
    label: str = "native-ui-mesa",
) -> subprocess.CompletedProcess[bytes]:
    """Run one shell-free, disposable fixture command with a hard bound."""

    if timeout <= 0:
        raise _error("Windows Mesa fixture command timeout is exhausted")
    if logs is not None:
        try:
            return _run_logged(
                command,
                cwd=cwd,
                logs=logs,
                label=label,
                timeout=timeout,
                check=True,
            )
        except LocalVMError as error:
            # The shared runner retains complete stdout/stderr, including
            # output emitted before a timeout or nonzero exit. Keep the Mesa
            # context in the exception without replacing those streams.
            raise _error(f"Windows Mesa fixture command failed: {error}") from error
    try:
        result = subprocess.run(
            command,
            cwd=str(cwd),
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
            check=False,
        )
    except FileNotFoundError as error:
        raise _error(f"Windows Mesa fixture tool is unavailable: {command[0]}") from error
    except subprocess.TimeoutExpired as error:
        stdout = getattr(error, "stdout", None) or b""
        stderr = getattr(error, "stderr", None) or b""
        detail = b"\n".join(part for part in (stdout, stderr) if part)
        suffix = f": {detail.decode('utf-8', errors='backslashreplace')}" if detail else ""
        raise _error(
            f"Windows Mesa fixture command timed out: {command[0]}{suffix}"
        ) from error
    except OSError as error:
        raise _error(f"Windows Mesa fixture command could not start: {command[0]}") from error
    if result.returncode != 0:
        details = []
        for stream, value in (("stdout", result.stdout), ("stderr", result.stderr)):
            if value:
                details.append(
                    f"{stream}: {value.decode('utf-8', errors='backslashreplace')}"
                )
        message = f"Windows Mesa fixture command failed: {command[0]} exited {result.returncode}"
        if details:
            message += ": " + " ".join(details)
        raise _error(message)
    # Focused callers may not have a run-log directory. Keep their successful
    # fixture diagnostics visible as explicit stream-delimited output rather
    # than silently discarding either stream. Production callers pass ``logs``
    # and the shared runner has already retained both complete streams there.
    emit_streams(label, result.stdout, result.stderr)
    return result


def _mesa_archive_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(block)
    except OSError as error:
        raise _error("Windows Mesa fixture archive could not be hashed") from error
    return digest.hexdigest()


def _prepare_mesa_llvmpipe_fixture(
    command: list[str], *, run_dir: Path, timeout: float, logs: Path | None = None,
) -> tuple[list[str], Path | None]:
    """Stage the exact Windows UI beside Mesa's two WGL DLLs.

    This is intentionally limited to the Windows full native-window command.
    The production UI is copied byte-for-byte to a disposable run directory;
    no installed file, registry value, System32 file, or machine environment
    variable is changed.  The archive is checksum-verified before ``tar.exe``
    sees it and only the two required archive members are extracted.
    """

    if "--ui" not in command:
        # The real full command always carries --ui.  Keeping this helper
        # tolerant makes focused process-wrapper tests independent of a
        # network download and does not weaken the production command builder,
        # which validates the candidate UI path before reaching this boundary.
        return list(command), None
    ui_index = command.index("--ui")
    if ui_index + 1 >= len(command) or not command[ui_index + 1]:
        raise _error("Windows native UI binary argument is missing")
    source = Path(command[ui_index + 1])
    if source.is_symlink() or not source.is_file():
        raise _error("Windows production UI binary is unavailable for Mesa staging")
    if timeout <= 0:
        raise _error("Windows native UI timeout must be positive")

    run_dir = run_dir.resolve()
    staging = _mesa_llvmpipe_staging_path(run_dir)
    archive = staging / _MESA_LLVMPIPE_ARCHIVE_NAME
    extraction = staging / ".extract"
    try:
        # A previous worker can have been terminated by the supervisor before
        # its finally block.  Remove that exact stale path before rebuilding.
        _remove_mesa_llvmpipe_staging(run_dir)
        staging.mkdir(parents=True, exist_ok=False)
        deadline = time.monotonic() + timeout

        def remaining() -> float:
            value = deadline - time.monotonic()
            if value <= 0:
                raise _error("Windows Mesa fixture preparation timed out")
            return value

        _run_mesa_fixture_command(
            [
                "curl.exe", "--fail", "--location", "--silent", "--show-error",
                "--retry", "2", "--connect-timeout", "15",
                "--max-time", str(max(1, int(remaining()))),
                "--output", str(archive), _MESA_LLVMPIPE_URL,
            ],
            cwd=run_dir,
            timeout=remaining(),
            logs=logs,
            label="native-ui-mesa-download",
        )
        if archive.is_symlink() or not archive.is_file() or archive.stat().st_size <= 0:
            raise _error("Windows Mesa fixture download did not produce an archive")
        observed_sha256 = _mesa_archive_sha256(archive)
        if observed_sha256 != _MESA_LLVMPIPE_SHA256:
            raise _error("Windows Mesa fixture archive checksum mismatch")

        # Supplying the two member names to tar is deliberate: no archive
        # wildcard or whole-archive extraction can add an unreviewed DLL.
        extraction.mkdir()
        _run_mesa_fixture_command(
            [
                "tar.exe", "-xf", str(archive), "-C", str(extraction),
                *_MESA_LLVMPIPE_MEMBERS,
            ],
            cwd=run_dir,
            timeout=remaining(),
            logs=logs,
            label="native-ui-mesa-extract",
        )
        for member in _MESA_LLVMPIPE_MEMBERS:
            extracted = extraction.joinpath(*member.split("/"))
            if extracted.is_symlink() or not extracted.is_file():
                raise _error(f"Windows Mesa fixture member is missing: {member}")
            destination = staging / Path(member).name
            shutil.copy2(extracted, destination)
            if destination.is_symlink() or not destination.is_file():
                raise _error(f"Windows Mesa fixture member could not be staged: {member}")

        destination_ui = staging / source.name
        shutil.copy2(source, destination_ui)
        if destination_ui.is_symlink() or not destination_ui.is_file():
            raise _error("Windows production UI copy did not complete")

        # The archive and temporary extraction tree are not needed while the
        # GUI runs.  Keeping only the UI and the two DLLs minimizes cleanup
        # surface and ensures no downloaded artifact is retained on success.
        archive.unlink()
        shutil.rmtree(extraction)
        staged_command = list(command)
        staged_command[ui_index + 1] = str(destination_ui)
        return staged_command, staging
    except Exception as error:
        try:
            _remove_mesa_llvmpipe_staging(run_dir)
        except Exception as cleanup_error:
            raise _error(
                f"{error}; Windows Mesa fixture cleanup failed: {cleanup_error}"
            ) from error
        raise


def _creation_ticks_match(expected: int, observed: int) -> bool:
    """Match Win32/.NET process times within WMI's sub-microsecond loss."""

    if (
        isinstance(expected, bool)
        or isinstance(observed, bool)
        or not isinstance(expected, int)
        or not isinstance(observed, int)
        or expected < 0
        or observed < 0
    ):
        return False
    return abs(expected - observed) < _NATIVE_UI_CREATION_TICK_TOLERANCE


def _validated_interactive_account(value: object, *, context: str) -> str:
    """Validate the configured non-SYSTEM Windows account used by the UI.

    The account is resolved by Windows during cleanup, so both ``user`` and
    ``DOMAIN\\user``/``.\\user`` forms remain valid.  Keep the value bounded
    before passing it to PowerShell and reject the built-in SYSTEM identity;
    cleanup must never turn a missing or SYSTEM alternate into a broad owner
    match.
    """

    if not isinstance(value, str):
        raise _error(f"{context} is not configured safely")
    account = value.strip()
    if not account or any(ord(character) < 0x20 or ord(character) == 0x7F for character in account):
        raise _error(f"{context} is not configured safely")
    if account.count("\\") > 1:
        raise _error(f"{context} is not configured safely")
    if "\\" in account:
        domain, user = (part.strip() for part in account.split("\\", 1))
        if not domain or not user:
            raise _error(f"{context} is not configured safely")
        account = f"{domain}\\{user}"
    else:
        user = account
    if user.casefold() == "system":
        raise _error(f"{context} is not configured safely")
    return account


_NATIVE_UI_PREFLIGHT_SCRIPT = r'''$ErrorActionPreference = "Stop"
$target = [string]$env:DOBBYVPN_CONTROL_TOKEN_USER
if ([string]::IsNullOrWhiteSpace($target)) {
  Write-Error "interactive user is not configured"
  exit 3
}
try {
  $targetAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $target
  $targetSid = $targetAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  Write-Error "interactive user cannot be resolved"
  exit 3
}

# Do not parse localized session-list output: it can report a disconnected
# session even while the user's Explorer processes are still present.  Resolve
# the configured account to a SID and use the unique Explorer session as the
# exact target for the console handoff.
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class DobbyVpnWts {
  [DllImport("kernel32.dll")]
  public static extern uint WTSGetActiveConsoleSessionId();
}
'@

function Get-ConfiguredExplorerProbe {
  $explorerProcesses = @()
  try {
    $explorerProcesses = @(Get-Process -Name "explorer" -IncludeUserName -ErrorAction Stop)
  } catch {
    # An absent Explorer is an expected unavailable-desktop result. Preserve
    # its complete provider diagnostic while continuing with an empty set;
    # unexpected provider failures remain fatal.
    if ($_.Exception.Message -notmatch "Cannot find a process|No process") { throw }
    [Console]::Error.WriteLine(($_ | Out-String))
  }
  $explorerMatches = @($explorerProcesses |
    ForEach-Object {
      $session = [int]$_.SessionId
      $owner = [string]$_.UserName
      if ($session -le 0 -or [string]::IsNullOrWhiteSpace($owner)) { return }
      try {
        $ownerAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $owner
        $ownerSid = $ownerAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
      } catch {
        throw
      }
      if ($ownerSid -eq $targetSid) {
        [pscustomobject]@{
          ProcessId = [int]$_.Id
          SessionId = $session
        }
      }
    })
  [pscustomobject]@{
    Processes = @($explorerMatches)
    Sessions = @($explorerMatches | Select-Object -ExpandProperty SessionId -Unique)
  }
}

function Get-ActiveConsoleSessionId {
  [uint32][DobbyVpnWts]::WTSGetActiveConsoleSessionId()
}

$probe = Get-ConfiguredExplorerProbe
$sessions = @($probe.Sessions)
if ($sessions.Count -ne 1) {
  Write-Error ("expected exactly one Explorer session for configured user; found {0}" -f $sessions.Count)
  exit 3
}
$session = [int]$sessions[0]
$activeConsole = Get-ActiveConsoleSessionId
if ($activeConsole -ne [uint32]$session) {
  # This command runs in the SYSTEM boundary.  Pass only the already validated
  # session ID and the fixed console destination; never disconnect or target a
  # broad user/session set.
  $tscon = Join-Path $env:SystemRoot "System32\tscon.exe"
  & $tscon ([string]$session) "/dest:console"
  if ($LASTEXITCODE -ne 0) {
    Write-Error ("validated Explorer session {0} could not be attached to the console" -f $session)
    exit 3
  }
}

# tscon is asynchronous from the point of view of WTS and Explorer.  Re-read
# both sides until the same validated user/session is the active console, so a
# scheduled task is never launched into a disconnected desktop.
$deadline = [DateTime]::UtcNow.AddSeconds(15)
$ready = $false
$explorerCount = 0
while ([DateTime]::UtcNow -lt $deadline) {
  $probe = Get-ConfiguredExplorerProbe
  $sessions = @($probe.Sessions)
  if ($sessions.Count -eq 1) {
    $observedSession = [int]$sessions[0]
    $activeConsole = Get-ActiveConsoleSessionId
    if ($observedSession -eq $session -and $activeConsole -eq [uint32]$session) {
      $ready = $true
      $explorerCount = @($probe.Processes).Count
      break
    }
  }
  Start-Sleep -Milliseconds 250
}
if (-not $ready) {
  Write-Error ("Explorer session {0} did not become the active console session" -f $session)
  exit 3
}
Write-Output ("ready|{0}|{1}" -f $session, $explorerCount)
'''


def _preflight_interactive_desktop(
    *, run_dir: Path, logs: Path, timeout: float, user: str,
) -> None:
    """Prove the scheduled task has a visible Explorer session before launch."""

    environment = os.environ.copy()
    environment["DOBBYVPN_CONTROL_TOKEN_USER"] = user
    try:
        result = _powershell(
            _NATIVE_UI_PREFLIGHT_SCRIPT,
            cwd=run_dir,
            logs=logs,
            label="native-ui-preflight",
            # The script has a bounded 15-second WTS/Explorer convergence
            # wait; leave process startup and tscon cleanup headroom around
            # that inner deadline.
            timeout=min(timeout, 30.0),
            environment=environment,
            check=False,
        )
    except Exception as error:
        # The command log retains the detailed PowerShell failure.  Keep the
        # result boundary free of scripts, paths, usernames, and raw stderr.
        raise WindowsInteractiveDesktopUnavailable(
            "Explorer desktop preflight failed before readiness check"
        ) from error
    if result.returncode != 0:
        if result.returncode == 3:
            raise WindowsInteractiveDesktopUnavailable(
                "no usable Explorer desktop session for configured interactive user"
            )
        status = result.returncode if isinstance(result.returncode, int) else "unknown"
        raise WindowsInteractiveDesktopUnavailable(
            f"Explorer desktop preflight exited with status {status}"
        )
    value = result.stdout.decode("ascii", errors="replace").strip()
    if not re.fullmatch(r"ready\|[1-9][0-9]*\|[1-9][0-9]*", value):
        raise WindowsInteractiveDesktopUnavailable(
            "Explorer desktop probe returned an invalid readiness record"
        )


_STOP_INSTALLED_SERVICE_SCRIPT = r'''$ErrorActionPreference = "Stop"
$service = Get-Service -Name "DobbyVPN Server" -ErrorAction Stop
if ($service.Status -ne "Stopped") {
  Stop-Service -InputObject $service -Force -ErrorAction Stop
  $service.WaitForStatus("Stopped", [TimeSpan]::FromSeconds(30))
}
$service = Get-Service -Name "DobbyVPN Server" -ErrorAction Stop
if ($service.Status -ne "Stopped") { throw "DobbyVPN Server did not stop" }
'''


def stop_installed_service(run_dir: Path, logs: Path, timeout: float) -> None:
    """Stop only the exact MSI-owned DobbyVPN service before direct launch."""
    _powershell(
        _STOP_INSTALLED_SERVICE_SCRIPT,
        cwd=run_dir,
        logs=logs,
        label="release-stop-service",
        timeout=min(timeout, 45.0),
    )


def _native_ui_wrapper(
    command: list[str],
    *,
    cwd: Path,
    environment: dict[str, str],
    stdout: Path,
    stderr: Path,
    pid: Path,
    child_pid: Path,
    exit_code: Path,
) -> str:
    """Build the user-session PowerShell wrapper for the native UI smoke.

    The wrapper starts the actual Python process with only the runtime values
    the desktop client needs.  ``ProcessStartInfo`` gives us an exact child
    PID so the SYSTEM worker can terminate the complete UI tree if the bounded
    wait expires.
    """

    if not command or any(not isinstance(value, str) or not value for value in command):
        raise _error("Windows native UI command is invalid")
    if not cwd.is_dir():
        raise _error("Windows native UI working directory is unavailable")
    executable = _powershell_literal(command[0])
    arguments = _powershell_literal(subprocess.list2cmdline(command[1:]))
    lines = [
        '$ErrorActionPreference = "Stop"',
        "$exitCode = 1",
        "try {",
        "  $info = New-Object System.Diagnostics.ProcessStartInfo",
        f"  $info.FileName = {executable}",
        f"  $info.Arguments = {arguments}",
        f"  $info.WorkingDirectory = {_powershell_literal(str(cwd))}",
        "  $info.UseShellExecute = $false",
        "  $info.RedirectStandardOutput = $true",
        "  $info.RedirectStandardError = $true",
        "  $userEnvironment = @{}",
        "  foreach ($name in @(" + ", ".join(
            _powershell_literal(name) for name in _NATIVE_UI_USER_ENVIRONMENT
        ) + ")) {",
        "    $value = [Environment]::GetEnvironmentVariable($name)",
        "    if (-not [string]::IsNullOrWhiteSpace($value)) { $userEnvironment[$name] = $value }",
        "  }",
        # ProcessStartInfo starts with the task's user environment.  Clear it
        # before applying the allow-list so credentials or unrelated runner
        # settings cannot leak into the product smoke process.
        "  $info.EnvironmentVariables.Clear()",
        "  foreach ($entry in $userEnvironment.GetEnumerator()) { $info.EnvironmentVariables[$entry.Key] = $entry.Value }",
        # The controller records the exact production UI PID immediately
        # after Popen.  The SYSTEM worker can therefore clean up an orphaned
        # UI even when the controller is killed before it sends ``ready``.
        f"  $info.EnvironmentVariables['DOBBYVPN_NATIVE_UI_CHILD_PID_FILE'] = "
        f"{_powershell_literal(str(child_pid))}",
    ]
    for key in sorted(_NATIVE_UI_ENVIRONMENT):
        value = environment.get(key)
        if value is not None:
            lines.append(
                f"  $info.EnvironmentVariables[{_powershell_literal(key)}] = "
                f"{_powershell_literal(value)}"
            )
    lines.extend([
        "  $process = $null",
        "  $process = New-Object System.Diagnostics.Process",
        "  $process.StartInfo = $info",
        "  if (-not $process.Start()) { throw 'native UI process did not start' }",
        "  $creationTicks = $process.StartTime.ToUniversalTime().Ticks",
        f"  Set-Content -LiteralPath {_powershell_literal(str(pid))} "
        "-Value (([string]$process.Id) + '|' + ([string]$creationTicks)) "
        "-Encoding ASCII -NoNewline",
        "  $stdoutTask = $process.StandardOutput.ReadToEndAsync()",
        "  $stderrTask = $process.StandardError.ReadToEndAsync()",
        "  $process.WaitForExit()",
        f"  [IO.File]::WriteAllText({_powershell_literal(str(stdout))}, $stdoutTask.Result)",
        f"  [IO.File]::WriteAllText({_powershell_literal(str(stderr))}, $stderrTask.Result)",
        "  $exitCode = $process.ExitCode",
    "} catch {",
        "  $failure = ($_ | Out-String)",
        "  if ($null -ne $process -and -not $process.HasExited) {",
        "    try {",
        "      $process.Kill()",
        "      $process.WaitForExit()",
        "    } catch {",
        "      $failure += [Environment]::NewLine + ($_ | Out-String)",
        "    }",
        "  }",
        f"  [IO.File]::AppendAllText({_powershell_literal(str(stderr))}, "
        "$failure + [Environment]::NewLine)",
        "  $exitCode = -1",
        "}",
        f"Set-Content -LiteralPath {_powershell_literal(str(exit_code))} "
        "-Value ([string]$exitCode) -Encoding ASCII -NoNewline",
    ])
    return "\n".join(lines) + "\n"


def _native_ui_register_script(
    *,
    task_name: str,
    user: str,
    wrapper: Path,
    cwd: Path,
) -> str:
    """Build the SYSTEM-side registration/start operation."""

    action_arguments = (
        "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "
        f'"{wrapper}"'
    )
    # A trigger is unnecessary for a task started with Start-ScheduledTask.
    # Keeping this task triggerless prevents a failed start from launching a
    # delayed UI while the caller is already cleaning up.
    return "\n".join([
        '$ErrorActionPreference = "Stop"',
        f"$principal = New-ScheduledTaskPrincipal -UserId {_powershell_literal(user)} "
        # ScheduledTasks names TASK_LOGON_INTERACTIVE_TOKEN "Interactive";
        # "InteractiveToken" is valid in task XML, not for this cmdlet enum.
        # The hosted native journey also owns routing-firewall preparation and
        # service-only process-loss recovery.  On the dedicated qualification
        # VM the configured interactive account is an administrator, so use
        # its existing elevated token rather than adding a second SYSTEM/UI
        # command boundary.
        "-LogonType Interactive -RunLevel Highest",
        f"$action = New-ScheduledTaskAction -Execute 'powershell.exe' "
        f"-Argument {_powershell_literal(action_arguments)} "
        f"-WorkingDirectory {_powershell_literal(str(cwd))}",
        f"Register-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-Action $action -Principal $principal -Force",
        f"Start-ScheduledTask -TaskName {_powershell_literal(task_name)}",
        # Start-ScheduledTask is asynchronous and normally returns success
        # even when Task Scheduler cannot create the user-session process.
        # Poll the fresh task's first run record so errors such as
        # ERROR_DIRECTORY (0x8007010b) fail at launch instead of looking like
        # a native UI timeout several minutes later.
        "$launchDeadline = (Get-Date).AddSeconds(5)",
        "$started = $false",
        "while ((Get-Date) -lt $launchDeadline) {",
        f"  $task = Get-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-ErrorAction Stop",
        f"  $info = Get-ScheduledTaskInfo -TaskName {_powershell_literal(task_name)} "
        "-ErrorAction Stop",
        "  if ($task.State -eq 'Running') {",
        "    $started = $true",
        "    break",
        "  }",
        "  if ($info.LastRunTime -gt [DateTime]::MinValue) {",
        "    $result = [uint32]$info.LastTaskResult",
        "    if ($result -ne 0) {",
        "      throw (\"scheduled native UI task failed before launch: 0x{0:X8}\" -f $result)",
        "    }",
        "    $started = $true",
        "    break",
        "  }",
        "  Start-Sleep -Milliseconds 100",
        "}",
        "if (-not $started) { throw 'scheduled native UI task did not start' }",
        "",
    ])


def _native_ui_access_script(
    *,
    user: str,
    run_dir: Path,
    cwd: Path,
    logs: Path,
    wrapper: Path,
    profile: Path,
    command: list[str],
    read_directories: tuple[Path, ...] = (),
) -> str:
    """Grant the interactive account bounded access to the disposable UI tree.

    The SYSTEM SSH worker creates the extracted run tree with an owner-only
    ACL.  Task Scheduler can therefore register an Interactive task but its
    user token cannot traverse the wrapper's directory, which surfaces as the
    opaque ``ERROR_DIRECTORY`` last-task result.  Grant access only to this
    run's source/profile/log paths and the concrete command paths; the source
    is read/execute, logs are modify, service PID/identity sidecars are
    modify, and the run root receives direct write access for the PID/exit
    markers.  The run tree is disposable and is removed by the normal SYSTEM
    cleanup boundary.
    """

    if not user:
        raise _error("Windows native UI access user is missing")
    source = cwd.parent if cwd.name.lower() == "torturer" else cwd
    read_path_flags = frozenset({
        "--cli", "--ui", "--profile", "--smoke-script", "--service-binary",
        "--service-library-path", "--raw-log-dir", "--output",
    })
    write_path_flags = frozenset({"--service-pid-file", "--service-identity-file"})
    read_paths: list[str] = [str(command[0]), str(wrapper)]
    write_paths: list[str] = []
    for index, value in enumerate(command[:-1]):
        if value in read_path_flags:
            read_paths.append(command[index + 1])
        elif value in write_path_flags:
            write_paths.append(command[index + 1])
    # These are mandatory paths whose existence is already established by the
    # local-VM run.  Command-derived paths are optional here so a later native
    # process check reports a missing candidate with its normal diagnostics.
    required_paths = (run_dir, source, logs, profile, wrapper)
    optional_paths = tuple(read_paths)
    lines = [
        '$ErrorActionPreference = "Stop"',
        f"$user = {_powershell_literal(user)}",
        "function Grant-Access {",
        "  param([string]$Path, [string]$Permission, [bool]$Recurse)",
        "  if (-not (Test-Path -LiteralPath $Path)) { throw \"native UI access path is missing\" }",
        "  $grant = \"{0}:{1}\" -f $user, $Permission",
        "  $arguments = @($Path, '/grant', $grant)",
        "  if ($Recurse) { $arguments += '/T' }",
        "  $arguments += '/C'",
        "  & \"$env:SystemRoot\\System32\\icacls.exe\" @arguments",
        "  if ($LASTEXITCODE -ne 0) { throw \"native UI access grant failed\" }",
        "}",
    ]
    for path in required_paths:
        lines.append(
            f"Grant-Access {_powershell_literal(str(path))} "
            f"{_powershell_literal('(OI)(CI)RX' if path in (source, logs) else 'RX')} "
            f"{'$true' if path in (source, logs) else '$false'}"
        )
    # Logs must be writable by the user-session process.  The source and
    # profile remain read-only; the run root's direct W grant permits only the
    # marker files created by the wrapper (no inherited write permission).
    lines.extend([
        f"Grant-Access {_powershell_literal(str(logs))} {_powershell_literal('(OI)(CI)M')} $true",
        f"Grant-Access {_powershell_literal(str(run_dir))} {_powershell_literal('W')} $false",
    ])
    for path in read_directories:
        lines.append(
            f"Grant-Access {_powershell_literal(str(path))} "
            f"{_powershell_literal('(OI)(CI)RX')} $true"
        )
    for path in optional_paths:
        lines.append(
            f"if (Test-Path -LiteralPath {_powershell_literal(path)}) {{ "
            f"Grant-Access {_powershell_literal(path)} {_powershell_literal('RX')} $false }}"
        )
    # The user-session process-loss controller rewrites these sidecars and
    # atomically replaces the identity marker after each service restart.
    # Grant only the existing files Modify; their parent is already writable
    # for creation of the temporary replacement file.
    for path in write_paths:
        lines.append(
            f"if (Test-Path -LiteralPath {_powershell_literal(path)}) {{ "
            f"Grant-Access {_powershell_literal(path)} {_powershell_literal('M')} $false }}"
        )
    return "\n".join(lines) + "\n"


def _native_ui_unregister_script(task_name: str) -> str:
    return "\n".join([
        '$ErrorActionPreference = "Stop"',
        "$task = $null",
        "try {",
        f"  $task = Get-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-ErrorAction Stop",
        "} catch {",
        "  $detail = $_ | Out-String",
        "  if ($detail -match 'cannot find|does not exist|not found|0x80070002') {",
        "    Write-Output $detail",
        "  } else { throw }",
        "}",
        "if ($null -ne $task) {",
        f"  Stop-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-ErrorAction Stop",
        f"  Unregister-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-Confirm:$false -ErrorAction Stop",
        "}",
        "",
    ])


_NATIVE_UI_KILL_SCRIPT = r'''$ErrorActionPreference = "Stop"
$expectedPathValue = [string]$env:DOBBYVPN_NATIVE_UI_CONTROLLER_BINARY
$expectedOwner = [string]$env:DOBBYVPN_NATIVE_UI_CONTROLLER_OWNER
if ([string]::IsNullOrWhiteSpace($expectedPathValue) -or [string]::IsNullOrWhiteSpace($expectedOwner)) {
  throw "native UI controller identity is not configured"
}
$expectedPath = [IO.Path]::GetFullPath($expectedPathValue)
try {
  $expectedAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $expectedOwner
  $expectedSid = $expectedAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  throw "native UI controller owner is invalid"
}
if ($expectedSid -eq "S-1-5-18") { throw "native UI controller owner must not be SYSTEM" }
$rawIdentity = [string]$env:DOBBYVPN_NATIVE_UI_PID
$parts = $rawIdentity -split '\|', 2
if ($parts.Count -ne 2 -or $parts[0] -notmatch '^[1-9][0-9]*$' -or $parts[1] -notmatch '^[1-9][0-9]+$') {
  throw "native UI controller identity is invalid"
}
$pidValue = [int]$parts[0]
$expectedCreationTicks = [int64]$parts[1]
$record = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue) -ErrorAction Stop
if ($null -eq $record) { exit 0 }
if ($null -eq $record.CreationDate -or [string]::IsNullOrWhiteSpace([string]$record.ExecutablePath)) {
  throw "native UI controller identity is unavailable"
}
if ([IO.Path]::GetFullPath([string]$record.ExecutablePath) -ine $expectedPath) {
  throw "native UI controller identity did not match the launcher"
}
$owner = Invoke-CimMethod -InputObject $record -MethodName GetOwner -ErrorAction Stop
if ($owner.ReturnValue -ne 0) { throw "native UI controller owner is unavailable" }
$ownerName = "{0}\{1}" -f [string]$owner.Domain, [string]$owner.User
try {
  $ownerAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $ownerName
  $ownerSid = $ownerAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  throw "native UI controller owner is unavailable"
}
if ($ownerSid -ne $expectedSid) { throw "native UI controller owner did not match the configured account" }
$observedCreationTicks = [int64]$record.CreationDate.ToUniversalTime().Ticks
$creationDelta = $observedCreationTicks - $expectedCreationTicks
if ($creationDelta -lt 0) { $creationDelta = -$creationDelta }
if ($creationDelta -ge 10) { throw "native UI controller identity did not match the launched process" }
& "$env:SystemRoot\System32\taskkill.exe" /PID $pidValue /T /F
if ($LASTEXITCODE -ne 0) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue)
  if ($null -ne $still) { throw "native UI controller process tree did not terminate" }
}
$deadline = [DateTime]::UtcNow.AddSeconds(15)
while ([DateTime]::UtcNow -lt $deadline) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue)
  if ($null -eq $still) { exit 0 }
  Start-Sleep -Milliseconds 100
}
throw "native UI controller process did not terminate"
'''


_NATIVE_UI_CHILD_KILL_SCRIPT = r'''$ErrorActionPreference = "Stop"
$pidPath = [string]$env:DOBBYVPN_NATIVE_UI_CHILD_PID_FILE
$expectedPathValue = [string]$env:DOBBYVPN_NATIVE_UI_CHILD_BINARY
$expectedOwner = [string]$env:DOBBYVPN_NATIVE_UI_CHILD_OWNER
if (
  [string]::IsNullOrWhiteSpace($pidPath) -or
  [string]::IsNullOrWhiteSpace($expectedPathValue) -or
  [string]::IsNullOrWhiteSpace($expectedOwner)
) {
  throw "native UI child identity is not configured"
}
$expectedPath = [IO.Path]::GetFullPath($expectedPathValue)
try {
  $expectedAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $expectedOwner
  $expectedSid = $expectedAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  throw "native UI child owner is invalid"
}
if ($expectedSid -eq "S-1-5-18") { throw "native UI child owner must not be SYSTEM" }
if (-not (Test-Path -LiteralPath $pidPath -PathType Leaf)) { exit 0 }
$rawIdentity = (Get-Content -LiteralPath $pidPath -Raw -ErrorAction Stop).Trim()
$parts = $rawIdentity -split '\|', 2
if ($parts.Count -ne 2 -or $parts[0] -notmatch '^[1-9][0-9]*$' -or $parts[1] -notmatch '^[1-9][0-9]+$') {
  throw "native UI child identity is invalid"
}
$pidValue = [int]$parts[0]
$expectedCreationTicks = [int64]$parts[1]
$record = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue) -ErrorAction Stop
if ($null -eq $record) { exit 0 }
if ($null -eq $record.CreationDate -or [string]::IsNullOrWhiteSpace([string]$record.ExecutablePath)) {
  throw "native UI child identity is unavailable"
}
if ([IO.Path]::GetFullPath([string]$record.ExecutablePath) -ine $expectedPath) {
  throw "native UI child identity did not match the launched binary"
}
$owner = Invoke-CimMethod -InputObject $record -MethodName GetOwner -ErrorAction Stop
if ($owner.ReturnValue -ne 0) { throw "native UI child owner is unavailable" }
$ownerName = "{0}\{1}" -f [string]$owner.Domain, [string]$owner.User
try {
  $ownerAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $ownerName
  $ownerSid = $ownerAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  throw "native UI child owner is unavailable"
}
if ($ownerSid -ne $expectedSid) { throw "native UI child owner did not match the configured account" }
$observedCreationTicks = [int64]$record.CreationDate.ToUniversalTime().Ticks
$creationDelta = $observedCreationTicks - $expectedCreationTicks
if ($creationDelta -lt 0) { $creationDelta = -$creationDelta }
if ($creationDelta -ge 10) {
  throw "native UI child identity did not match the launched process"
}
& "$env:SystemRoot\System32\taskkill.exe" /PID $pidValue /T /F
if ($LASTEXITCODE -ne 0) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue)
  if ($null -ne $still) { throw "native UI child process tree did not terminate" }
}
$deadline = [DateTime]::UtcNow.AddSeconds(15)
while ([DateTime]::UtcNow -lt $deadline) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue)
  if ($null -eq $still) { exit 0 }
  Start-Sleep -Milliseconds 100
}
throw "native UI child process did not terminate"
'''


def run_interactive_ui(
    command: list[str],
    *,
    run_dir: Path,
    cwd: Path,
    logs: Path,
    timeout: float,
    environment: dict[str, str],
) -> subprocess.CompletedProcess[bytes]:
    """Run the real Windows UI in the logged-in user's interactive session.

    The local VM worker remains SYSTEM and owns the supervised session and
    cleanup.  The bounded native journey runs through the configured installed
    administrator's interactive token so its real window, routing preparation,
    and service-only recovery share one desktop context.  No password or second
    service boundary is introduced.
    """

    from .local_vm import LocalVMError

    if timeout <= 0:
        raise LocalVMError("Windows native UI timeout must be positive")
    run_dir = run_dir.resolve()
    cwd = cwd.resolve()
    logs = logs.resolve()
    logs.mkdir(parents=True, exist_ok=True)
    user = _validated_interactive_account(
        environment.get("DOBBYVPN_CONTROL_TOKEN_USER"),
        context="Windows interactive UI user",
    )
    # Keep the task and all markers inside this disposable candidate.  The
    # wrapper itself is never exposed outside the VM run directory.
    suffix = uuid.uuid4().hex
    task_name = f"DobbyVPN-Torturer-NativeUI-{suffix}"
    wrapper = run_dir / "native-ui-task.ps1"
    stdout = logs / "native-ui.stdout.log"
    stderr = logs / "native-ui.stderr.log"
    pid = run_dir / "native-ui.pid"
    child_pid = run_dir / "native-ui-child.pid"
    exit_code = run_dir / "native-ui.exit"
    controller_binary = command[0] if command else ""
    try:
        ui_flag = command.index("--ui")
        requested_ui_binary = command[ui_flag + 1]
    except (ValueError, IndexError):
        requested_ui_binary = None
    if requested_ui_binary is not None and (
        not isinstance(requested_ui_binary, str) or not requested_ui_binary
    ):
        raise LocalVMError("Windows native UI binary argument is invalid")
    filtered_environment = {
        key: str(value)
        for key, value in environment.items()
        if key in _NATIVE_UI_ENVIRONMENT
        and key != "GALLIUM_DRIVER"
        and isinstance(value, str)
    }
    if filtered_environment.get("DOBBYVPN_CONTROL_TOKEN_USER") != user:
        raise LocalVMError("Windows interactive UI control-token user is invalid")
    # A previous supervisor timeout can leave only the disposable Mesa tree
    # behind.  Remove that fixed path before probing or downloading anything.
    _remove_mesa_llvmpipe_staging(run_dir)
    # A SYSTEM worker can register an interactive task even when the target
    # account has no visible shell.  Prove the user's Explorer session first;
    # otherwise the wait for the task's exit marker can consume the full lane
    # timeout without ever starting the production window.
    _preflight_interactive_desktop(
        run_dir=run_dir, logs=logs, timeout=timeout, user=user,
    )
    for path in (pid, child_pid, exit_code, stdout, stderr):
        try:
            path.unlink()
        except FileNotFoundError:
            pass
        except OSError as error:
            raise LocalVMError(f"Windows native UI marker is not removable: {path.name}") from error
    staged_command, staging = _prepare_mesa_llvmpipe_fixture(
        command, run_dir=run_dir, timeout=timeout, logs=logs,
    )
    if staging is not None:
        filtered_environment["GALLIUM_DRIVER"] = "llvmpipe"
    try:
        staged_ui_flag = staged_command.index("--ui")
        ui_binary = staged_command[staged_ui_flag + 1]
    except (ValueError, IndexError):
        ui_binary = None
    if ui_binary is not None and (
        not isinstance(ui_binary, str) or not ui_binary
    ):
        raise LocalVMError("Windows native UI binary argument is invalid")
    registered = False
    failure: Exception | None = None
    child_cleanup_attempted = False

    def cleanup_child() -> None:
        nonlocal child_cleanup_attempted
        if child_cleanup_attempted or not child_pid.is_file() or ui_binary is None:
            return
        child_cleanup_attempted = True
        child_environment = os.environ.copy()
        child_environment.update({
            "DOBBYVPN_NATIVE_UI_CHILD_PID_FILE": str(child_pid),
            "DOBBYVPN_NATIVE_UI_CHILD_BINARY": ui_binary,
            "DOBBYVPN_NATIVE_UI_CHILD_OWNER": user,
        })
        _powershell(
            _NATIVE_UI_CHILD_KILL_SCRIPT,
            cwd=run_dir,
            logs=logs,
            label="native-ui-child-kill",
            timeout=min(timeout, 20.0),
            environment=child_environment,
        )

    try:
        wrapper.write_text(
            _native_ui_wrapper(
                staged_command,
                cwd=cwd,
                environment=filtered_environment,
                stdout=stdout,
                stderr=stderr,
                pid=pid,
                child_pid=child_pid,
                exit_code=exit_code,
            ),
            encoding="utf-8",
        )
        _powershell(
            _native_ui_access_script(
                user=user,
                run_dir=run_dir,
                cwd=cwd,
                logs=logs,
                wrapper=wrapper,
                profile=run_dir / "profile",
                command=staged_command,
                read_directories=(staging,) if staging is not None else (),
            ),
            cwd=run_dir,
            logs=logs,
            label="native-ui-access",
            timeout=min(timeout, 60.0),
        )
        # Register-ScheduledTask and Start-ScheduledTask are one PowerShell
        # operation.  Mark the task as potentially present before entering it
        # so a successful registration followed by a failed Start is still
        # cleaned up.
        registered = True
        _powershell(
            _native_ui_register_script(
                task_name=task_name, user=user, wrapper=wrapper, cwd=cwd,
            ),
            cwd=run_dir,
            logs=logs,
            label="native-ui-task-register",
            timeout=min(timeout, 30.0),
        )
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline and not exit_code.is_file():
            time.sleep(min(0.1, max(0.01, deadline - time.monotonic())))
        if not exit_code.is_file():
            if pid.is_file():
                try:
                    value = pid.read_text(encoding="ascii").strip()
                except OSError as error:
                    raise LocalVMError("Windows native UI PID marker is unreadable") from error
                if not _IDENTITY.fullmatch(value):
                    raise LocalVMError("Windows native UI controller identity marker is invalid")
                kill_environment = os.environ.copy()
                kill_environment["DOBBYVPN_NATIVE_UI_PID"] = value
                kill_environment["DOBBYVPN_NATIVE_UI_CONTROLLER_BINARY"] = controller_binary
                kill_environment["DOBBYVPN_NATIVE_UI_CONTROLLER_OWNER"] = user
                _powershell(
                    _NATIVE_UI_KILL_SCRIPT,
                    cwd=run_dir,
                    logs=logs,
                    label="native-ui-kill",
                    timeout=min(timeout, 15.0),
                    environment=kill_environment,
                )
            cleanup_child()
            raise LocalVMError("Windows native UI task timed out")
        try:
            raw_exit_code = exit_code.read_text(encoding="ascii").strip()
        except OSError as error:
            raise LocalVMError("Windows native UI exit marker is unreadable") from error
        if not re.fullmatch(r"-?[0-9]+", raw_exit_code):
            raise LocalVMError("Windows native UI exit marker is invalid")
        returncode = int(raw_exit_code)
        return subprocess.CompletedProcess(
            staged_command,
            returncode,
            stdout.read_bytes() if stdout.is_file() else b"",
            stderr.read_bytes() if stderr.is_file() else b"",
        )
    except Exception as error:
        failure = error
        raise
    finally:
        cleanup_failures: list[str] = []
        try:
            cleanup_child()
        except Exception as cleanup_error:
            cleanup_failures.append(f"child cleanup failed: {cleanup_error}")
        if registered:
            try:
                _powershell(
                    _native_ui_unregister_script(task_name),
                    cwd=run_dir,
                    logs=logs,
                    label="native-ui-task-cleanup",
                    timeout=min(timeout, 30.0),
                )
            except Exception as cleanup_error:
                cleanup_failures.append(f"task cleanup failed: {cleanup_error}")
        for path in (wrapper, pid, child_pid, exit_code):
            try:
                path.unlink()
            except FileNotFoundError:
                pass
            except OSError as cleanup_error:
                cleanup_failures.append(
                    f"marker cleanup failed ({path.name}): "
                    f"{type(cleanup_error).__name__}: {cleanup_error}"
                )
        try:
            _remove_mesa_llvmpipe_staging(run_dir)
        except Exception as cleanup_error:
            cleanup_failures.append(f"Mesa fixture cleanup failed: {cleanup_error}")
        if cleanup_failures:
            detail = "; ".join(cleanup_failures)
            if failure is None:
                raise LocalVMError(f"Windows native UI cleanup failed: {detail}")
            raise LocalVMError(f"{failure}; Windows native UI {detail}") from failure


def _discover_network_interface(run_dir: Path, logs: Path, timeout: float) -> str:
    # This is the same proof used by the hosted Windows routing adapter: one
    # unique default IPv4 route and an Up adapter.  The functional adapter
    # consumes the numeric interface index, not an alias that can be renamed.
    script = r'''$ErrorActionPreference = "Stop"
$routes = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix "0.0.0.0/0" -ErrorAction Stop | Sort-Object RouteMetric)
$indices = @($routes | ForEach-Object { $_.InterfaceIndex } | Select-Object -Unique)
if ($indices.Count -ne 1) { throw "uplink is ambiguous" }
$adapter = Get-NetAdapter -InterfaceIndex $indices[0] -ErrorAction Stop
if ($adapter.Status -ne "Up") { throw "uplink is not up" }
Write-Output ([string]$indices[0])
'''
    result = _powershell(
        script, cwd=run_dir, logs=logs, label="network-interface", timeout=min(timeout, 15.0)
    )
    value = result.stdout.decode("ascii", errors="strict").strip()
    if not _INTERFACE.fullmatch(value):
        raise _error("Windows uplink probe returned an invalid interface index")
    return value


_IDENTITY_SCRIPT = r'''$ErrorActionPreference = "Stop"
$record = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f ([int]$env:DOBBYVPN_SERVICE_PID)) -ErrorAction Stop
if ($null -eq $record -or $null -eq $record.CreationDate -or [string]::IsNullOrWhiteSpace([string]$record.ExecutablePath)) {
  throw "service identity unavailable"
}
$owner = Invoke-CimMethod -InputObject $record -MethodName GetOwner -ErrorAction Stop
if ($owner.ReturnValue -ne 0) { throw "service owner unavailable" }
[ordered]@{
  pid = [int]$record.ProcessId
  creation_ticks = [string]$record.CreationDate.ToUniversalTime().Ticks
  executable_path = [string]$record.ExecutablePath
  owner_user = [string]$owner.User
  owner_domain = [string]$owner.Domain
} | ConvertTo-Json -Compress
'''


def _query_identity(pid: int, binary: Path, *, run_dir: Path, logs: Path, timeout: float) -> str:
    environment = os.environ.copy()
    environment["DOBBYVPN_SERVICE_PID"] = str(pid)
    result = _powershell(
        _IDENTITY_SCRIPT,
        cwd=run_dir,
        logs=logs,
        label="service-identity",
        timeout=min(timeout, 15.0),
        environment=environment,
    )
    try:
        value = json.loads(result.stdout.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise _error("Windows service identity is invalid") from error
    if not isinstance(value, dict):
        raise _error("Windows service identity is invalid")
    ticks = value.get("creation_ticks")
    observed_path = value.get("executable_path")
    if (
        value.get("pid") != pid
        or not isinstance(ticks, str)
        or not re.fullmatch(r"[1-9][0-9]+", ticks)
        or not isinstance(observed_path, str)
        or os.path.normcase(os.path.abspath(observed_path)) != os.path.normcase(str(binary))
        or str(value.get("owner_user", "")).casefold() != "system"
        or str(value.get("owner_domain", "")).casefold() != "nt authority"
    ):
        raise _error("Windows service identity did not match the SYSTEM candidate")
    return f"{pid}|{ticks}"


def _wait_ready(run_dir: Path, logs: Path, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", 50051), timeout=min(0.5, timeout)):
                return
        except OSError:
            time.sleep(min(0.25, max(0.0, deadline - time.monotonic())))
    raise _error("Windows candidate service did not become ready")


def start(run_dir: Path, descriptor: dict[str, Any], logs: Path, timeout: float) -> dict[str, Any]:
    """Start the built service directly under the SYSTEM session boundary."""

    binary = _service_path(descriptor)
    run_dir = run_dir.resolve()
    service_log = logs / "service.log"
    service_stdout = logs / "service.stdout.log"
    service_stderr = logs / "service.stderr.log"
    pid_file = run_dir / "service.pid"
    identity_file = run_dir / "service.identity"
    runtime: dict[str, Any] = {
        "binary": str(binary),
        "socket": _CONTROL_ADDRESS,
        "control_address": _CONTROL_ADDRESS,
        "pid_file": str(pid_file),
        "identity_file": str(identity_file),
        "service_log": str(service_log),
        "network_interface": None,
    }
    # Persist ownership before probing, opening, or launching anything.
    _save_state(run_dir, runtime)
    logs.mkdir(parents=True, exist_ok=True)
    for path in (service_log, service_stdout, service_stderr):
        path.touch(exist_ok=True)

    interface = _discover_network_interface(run_dir, logs, timeout)
    runtime["network_interface"] = interface
    _save_state(run_dir, runtime)

    environment = os.environ.copy()
    environment.update({
        "PROGRAMDATA": str(run_dir / "ProgramData"),
        "DOBBYVPN_CONTROL_ADDRESS": _CONTROL_ADDRESS,
        "DOBBY_LOG_PATH": str(service_log),
        "DOBBY_LOG_ROOT": str(logs),
        "DOBBY_LOG_PRECREATED": "1",
        # Preserve the documented local Windows workaround unless the guest
        # deliberately supplied a different value.
        "GODEBUG": environment.get("GODEBUG", "asyncpreemptoff=1"),
    })
    # controlplane/token_windows.go rejects SYSTEM as the installed-user ACL
    # identity.  Provisioning supplies this value; fail before launch when a
    # SYSTEM task would otherwise make an unusable token.
    token_user = _validated_interactive_account(
        environment.get("DOBBYVPN_CONTROL_TOKEN_USER"),
        context="Windows control-token user",
    )
    # The functional CLI is launched by the same local-VM command but needs
    # the service's control-token and PROGRAMDATA settings as well.  Keep a
    # small allow-list in state instead of serializing the whole guest env.
    runtime["environment"] = {
        key: environment[key]
        for key in (
            "PROGRAMDATA", "DOBBYVPN_CONTROL_ADDRESS", "DOBBYVPN_CONTROL_TOKEN_USER",
            "DOBBY_LOG_PATH", "DOBBY_LOG_ROOT", "DOBBY_LOG_PRECREATED", "GODEBUG",
        )
    }
    _save_state(run_dir, runtime)

    try:
        with service_stdout.open("ab") as stdout, service_stderr.open("ab") as stderr:
            process = subprocess.Popen(
                [str(binary), "-port", "50051"],
                cwd=str(binary.parent),
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=stdout,
                stderr=stderr,
            )
        pid = int(process.pid)
        if not _PID.fullmatch(str(pid)):
            raise _error("Windows service returned an invalid PID")
        runtime["pid"] = pid
        # Persist immediately after Popen: an identity probe failure must
        # still leave enough information for a safe exact cleanup.
        _save_state(run_dir, runtime)
        identity = _query_identity(pid, binary, run_dir=run_dir, logs=logs, timeout=timeout)
        pid_file.write_text(f"{pid}\n", encoding="ascii")
        identity_file.write_text(identity + "\n", encoding="ascii")
        runtime["identity"] = identity
        _save_state(run_dir, runtime)
        _wait_ready(run_dir, logs, timeout)
        return runtime
    except Exception:
        # Do not attempt an unproved kill here.  The supervisor will invoke
        # cleanup with the persisted PID/identity, or report that proof is
        # unavailable rather than touching an unrelated process.
        raise


_STOP_SCRIPT = r'''$ErrorActionPreference = "Stop"
$pidValue = [int]$env:DOBBYVPN_SERVICE_PID
$expected = [string]$env:DOBBYVPN_SERVICE_IDENTITY
$expectedPath = [IO.Path]::GetFullPath($env:DOBBYVPN_SERVICE_BINARY)
$expectedOwner = [string]$env:DOBBYVPN_SERVICE_INTERACTIVE_OWNER
if ([string]::IsNullOrWhiteSpace($expectedOwner)) { throw "service alternate owner is not configured" }
try {
  $expectedAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $expectedOwner
  $expectedSid = $expectedAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  throw "service alternate owner is invalid"
}
if ($expectedSid -eq "S-1-5-18") { throw "service alternate owner must not be SYSTEM" }
$record = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue) -ErrorAction Stop
if ($null -eq $record) { exit 0 }
if ($null -eq $record.CreationDate) { throw "service creation time unavailable" }
$observed = [string]$record.ProcessId + "|" + [string]$record.CreationDate.ToUniversalTime().Ticks
if ($observed -ne $expected) { exit 0 }
if ([IO.Path]::GetFullPath([string]$record.ExecutablePath) -ine $expectedPath) { exit 0 }
$owner = Invoke-CimMethod -InputObject $record -MethodName GetOwner -ErrorAction Stop
if ($owner.ReturnValue -ne 0) { throw "service owner unavailable" }
$ownerName = "{0}\{1}" -f [string]$owner.Domain, [string]$owner.User
try {
  $ownerAccount = New-Object -TypeName System.Security.Principal.NTAccount -ArgumentList $ownerName
  $ownerSid = $ownerAccount.Translate([System.Security.Principal.SecurityIdentifier]).Value
} catch {
  throw "service owner unavailable"
}
if ($ownerSid -ne "S-1-5-18" -and $ownerSid -ne $expectedSid) { exit 0 }
& "$env:SystemRoot\System32\taskkill.exe" /PID $pidValue /T /F
if ($LASTEXITCODE -ne 0) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue)
  if ($null -ne $still) { throw "service taskkill failed" }
}
$deadline = [DateTime]::UtcNow.AddSeconds(15)
while ([DateTime]::UtcNow -lt $deadline) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue)
  if ($null -eq $still) { exit 0 }
  Start-Sleep -Milliseconds 100
}
throw "service did not terminate"
'''


def _cleanup_interactive_owner(runtime: dict[str, Any]) -> str:
    """Return the exact non-SYSTEM account permitted for service cleanup."""

    environment = runtime.get("environment")
    if not isinstance(environment, dict):
        raise _error("Windows cleanup interactive owner is not configured")
    return _validated_interactive_account(
        environment.get("DOBBYVPN_CONTROL_TOKEN_USER"),
        context="Windows cleanup interactive owner",
    )


_REMOVE_FIREWALL_SCRIPT = rf'''$ErrorActionPreference = "Stop"
$name = "{_FIREWALL_RULE}"
Get-NetFirewallRule -ErrorAction Stop |
  Where-Object {{ $_.Name -eq $name }} |
  Remove-NetFirewallRule -ErrorAction Stop
'''


_ENABLE_INTERFACE_SCRIPT = r'''$ErrorActionPreference = "Stop"
$index = [int]$env:DOBBYVPN_NETWORK_INTERFACE
if ($index -le 0) { throw "network interface index is invalid" }
$adapters = @(Get-NetAdapter -ErrorAction Stop | Where-Object { [int]$_.ifIndex -eq $index })
if ($adapters.Count -ne 1) { throw "recorded network adapter is absent or ambiguous" }
$adapters | Enable-NetAdapter -Confirm:$false -ErrorAction Stop
$adapter = Get-NetAdapter -InterfaceIndex $index -ErrorAction Stop
if ($adapter.Status -ne "Up") { throw "recorded network adapter is not up" }
'''


def cleanup(run_dir: Path, runtime: dict[str, Any], logs: Path, timeout: float) -> None:
    """Stop only the recorded SYSTEM/UI-owner process and remove the test rule.

    The process identity proof remains the recorded PID, creation ticks, and
    exact executable path.  Its owner must be SYSTEM (the startup owner) or
    the exact configured interactive test account; no other administrator or
    user is eligible for termination.
    """

    run_dir = run_dir.resolve()
    logs.mkdir(parents=True, exist_ok=True)
    errors: list[str] = []
    pid = runtime.get("pid")
    if not isinstance(pid, int) or pid <= 0:
        pid = None
    pid_path = Path(runtime["pid_file"]) if isinstance(runtime.get("pid_file"), str) else run_dir / "service.pid"
    identity_path = Path(runtime["identity_file"]) if isinstance(runtime.get("identity_file"), str) else run_dir / "service.identity"
    binary = Path(runtime["binary"]) if isinstance(runtime.get("binary"), str) else None
    if (
        isinstance(pid, int) and pid > 0
        or pid_path.is_file()
        or identity_path.is_file()
        or isinstance(runtime.get("identity"), str)
    ):
        try:
            # The sidecars are the live ownership record.  A process-loss
            # scenario can replace the service and update these files while
            # platform.json still contains the original PID/identity.
            sidecar_pid: int | None = None
            if pid_path.is_file():
                try:
                    value = pid_path.read_text(encoding="ascii").strip()
                except OSError as error:
                    raise _error("Windows service PID file is unavailable") from error
                if not _PID.fullmatch(value):
                    raise _error("Windows service PID file is invalid")
                sidecar_pid = int(value)
                pid = sidecar_pid
            elif pid_path.exists():
                raise _error("Windows service PID file is unavailable")

            identity: str | None = None
            if identity_path.is_file():
                try:
                    identity = identity_path.read_text(encoding="ascii").strip()
                except OSError as error:
                    raise _error("Windows service identity file is unavailable") from error
            elif identity_path.exists():
                raise _error("Windows service identity file is unavailable")
            else:
                # Older live runs may have lost their sidecars during a
                # failed cleanup, but platform.json retained the validated
                # identity.  It is safe to recover only from that complete
                # identity; a bare stale runtime PID is never kill authority.
                saved_identity = runtime.get("identity")
                if isinstance(saved_identity, str):
                    identity = saved_identity.strip()

            if identity is None:
                raise _error("Windows service identity files are unavailable")
            if not _IDENTITY.fullmatch(identity):
                raise _error("Windows service identity file is invalid")
            identity_pid = int(identity.split("|", 1)[0])
            if sidecar_pid is None:
                # A saved identity is stronger than the stale runtime PID:
                # restart scenarios may update one while platform.json still
                # describes the original process.
                pid = identity_pid
            elif identity_pid != pid:
                raise _error("Windows service identity file is invalid")
            if binary is None or not binary.is_file():
                raise _error("Windows service binary is unavailable")
            interactive_owner = _cleanup_interactive_owner(runtime)
            env = os.environ.copy()
            env.update({
                "DOBBYVPN_SERVICE_PID": str(pid),
                "DOBBYVPN_SERVICE_IDENTITY": identity,
                "DOBBYVPN_SERVICE_BINARY": str(binary.resolve()),
                "DOBBYVPN_SERVICE_INTERACTIVE_OWNER": interactive_owner,
            })
            _powershell(_STOP_SCRIPT, cwd=run_dir, logs=logs, label="cleanup-service", timeout=timeout, environment=env)
        except Exception as error:
            errors.append(f"cleanup-service: {type(error).__name__}: {error}")
    try:
        _powershell(_REMOVE_FIREWALL_SCRIPT, cwd=run_dir, logs=logs, label="cleanup-routing", timeout=timeout)
    except Exception as error:
        errors.append(f"cleanup-routing: {type(error).__name__}: {error}")
    interface = runtime.get("network_interface")
    if interface is not None:
        if not isinstance(interface, str) or not _INTERFACE.fullmatch(interface):
            errors.append("cleanup-network-interface: recorded interface index is invalid")
        else:
            environment = os.environ.copy()
            environment["DOBBYVPN_NETWORK_INTERFACE"] = interface
            try:
                _powershell(
                    _ENABLE_INTERFACE_SCRIPT,
                    cwd=run_dir,
                    logs=logs,
                    label="cleanup-network-interface",
                    timeout=timeout,
                    environment=environment,
                )
            except Exception as error:
                errors.append(f"cleanup-network-interface: {type(error).__name__}: {error}")
    # The supervisor runs this cleanup command even when the native UI worker
    # was terminated by its outer timeout.  Keep the fixed Mesa path out of
    # retained run directories without touching any installed/release files.
    try:
        _remove_mesa_llvmpipe_staging(run_dir)
    except Exception as error:
        errors.append(f"cleanup-mesa-fixture: {type(error).__name__}: {error}")
    if errors:
        raise _error("; ".join(errors))
