"""Native Windows candidate lifecycle used by :mod:`local_vm`.

The SSH session itself is the privileged SYSTEM boundary on Windows.  The
candidate service is therefore an ordinary child process of that boundary;
there is no second Task Scheduler task to leak when setup fails.  All process
ownership needed by a later cleanup is recorded in ``platform.json`` before
the corresponding side effect.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import socket
import subprocess
import time
from typing import Any
import uuid

from .local_vm import LocalVMError

_PID = re.compile(r"^[1-9][0-9]*$")
_IDENTITY = re.compile(r"^[1-9][0-9]*\|[1-9][0-9]+$")
_INTERFACE = re.compile(r"^[1-9][0-9]*$")
_CONTROL_ADDRESS = "127.0.0.1:50051"
_FIREWALL_RULE = "DobbyVPN-Torturer-Routing-Probe"
_NATIVE_UI_ENVIRONMENT = frozenset({
    "PROGRAMDATA",
    "DOBBYVPN_CONTROL_ADDRESS",
    "DOBBYVPN_CONTROL_TOKEN_USER",
    "DOBBY_LOG_PATH",
    "DOBBY_LOG_ROOT",
    "DOBBY_LOG_PRECREATED",
    "GODEBUG",
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


_NATIVE_UI_PREFLIGHT_SCRIPT = r'''$ErrorActionPreference = "Stop"
$target = [string]$env:DOBBYVPN_CONTROL_TOKEN_USER
if ([string]::IsNullOrWhiteSpace($target)) {
  Write-Error "interactive user is not configured"
  exit 3
}
$shortName = ($target -split "\\")[-1]
$explorers = @(Get-Process -Name "explorer" -IncludeUserName -ErrorAction SilentlyContinue |
  Where-Object {
    $session = [int]$_.SessionId
    $owner = [string]$_.UserName
    $session -gt 0 -and (
      $owner -ieq $target -or
      $owner -ieq ("{0}\{1}" -f $env:COMPUTERNAME, $shortName) -or
      $owner -imatch ("\\{0}$" -f [regex]::Escape($shortName))
    )
  })
if ($explorers.Count -eq 0) {
  Write-Error ("no Explorer desktop session for {0}" -f $target)
  exit 3
}
$session = [int]$explorers[0].SessionId
Write-Output ("ready|{0}|{1}" -f $session, $explorers.Count)
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
            timeout=min(timeout, 15.0),
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
        "  $info.CreateNoWindow = $true",
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
    ]
    for key in sorted(_NATIVE_UI_ENVIRONMENT):
        value = environment.get(key)
        if value is not None:
            lines.append(
                f"  $info.EnvironmentVariables[{_powershell_literal(key)}] = "
                f"{_powershell_literal(value)}"
            )
    lines.extend([
        "  $process = New-Object System.Diagnostics.Process",
        "  $process.StartInfo = $info",
        "  if (-not $process.Start()) { throw 'native UI process did not start' }",
        f"  Set-Content -LiteralPath {_powershell_literal(str(pid))} "
        "-Value ([string]$process.Id) -Encoding ASCII -NoNewline",
        "  $stdoutTask = $process.StandardOutput.ReadToEndAsync()",
        "  $stderrTask = $process.StandardError.ReadToEndAsync()",
        "  $process.WaitForExit()",
        f"  [IO.File]::WriteAllText({_powershell_literal(str(stdout))}, $stdoutTask.Result)",
        f"  [IO.File]::WriteAllText({_powershell_literal(str(stderr))}, $stderrTask.Result)",
        "  $exitCode = $process.ExitCode",
        "} catch {",
        f"  [IO.File]::AppendAllText({_powershell_literal(str(stderr))}, "
        "($_ | Out-String) + [Environment]::NewLine)",
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
        "-LogonType Interactive -RunLevel Limited",
        f"$action = New-ScheduledTaskAction -Execute 'powershell.exe' "
        f"-Argument {_powershell_literal(action_arguments)} "
        f"-WorkingDirectory {_powershell_literal(str(cwd))}",
        f"Register-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-Action $action -Principal $principal -Force | Out-Null",
        f"Start-ScheduledTask -TaskName {_powershell_literal(task_name)}",
        "",
    ])


def _native_ui_unregister_script(task_name: str) -> str:
    return "\n".join([
        '$ErrorActionPreference = "Stop"',
        f"$task = Get-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-ErrorAction SilentlyContinue",
        "if ($null -ne $task) {",
        f"  Stop-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-ErrorAction SilentlyContinue",
        f"  Unregister-ScheduledTask -TaskName {_powershell_literal(task_name)} "
        "-Confirm:$false -ErrorAction Stop",
        "}",
        "",
    ])


_NATIVE_UI_KILL_SCRIPT = r'''$ErrorActionPreference = "Stop"
$pidValue = [int]$env:DOBBYVPN_NATIVE_UI_PID
if ($pidValue -le 0) { throw "native UI PID is invalid" }
$process = Get-Process -Id $pidValue -ErrorAction SilentlyContinue
if ($null -ne $process) {
  & "$env:SystemRoot\System32\taskkill.exe" /PID $pidValue /T /F | Out-Null
  if ($LASTEXITCODE -ne 0 -and $null -ne (Get-Process -Id $pidValue -ErrorAction SilentlyContinue)) {
    throw "native UI process tree did not terminate"
  }
}
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

    The local VM worker remains SYSTEM and owns the VPN service.  Only this
    short-lived UI smoke is delegated to the configured installed user through
    Task Scheduler's existing interactive token; no password or second service
    boundary is introduced.
    """

    from .local_vm import LocalVMError

    if timeout <= 0:
        raise LocalVMError("Windows native UI timeout must be positive")
    run_dir = run_dir.resolve()
    cwd = cwd.resolve()
    logs = logs.resolve()
    logs.mkdir(parents=True, exist_ok=True)
    user = str(environment.get("DOBBYVPN_CONTROL_TOKEN_USER", "")).strip()
    if not user or user.upper() in {"SYSTEM", "NT AUTHORITY\\SYSTEM"}:
        raise LocalVMError("Windows interactive UI user is not configured")
    # Keep the task and all markers inside this disposable candidate.  The
    # wrapper itself is never exposed outside the VM run directory.
    suffix = uuid.uuid4().hex
    task_name = f"DobbyVPN-Torturer-NativeUI-{suffix}"
    wrapper = run_dir / "native-ui-task.ps1"
    stdout = logs / "native-ui.stdout.log"
    stderr = logs / "native-ui.stderr.log"
    pid = run_dir / "native-ui.pid"
    exit_code = run_dir / "native-ui.exit"
    filtered_environment = {
        key: str(value)
        for key, value in environment.items()
        if key in _NATIVE_UI_ENVIRONMENT and isinstance(value, str)
    }
    if filtered_environment.get("DOBBYVPN_CONTROL_TOKEN_USER") != user:
        raise LocalVMError("Windows interactive UI control-token user is invalid")
    # A SYSTEM worker can register an interactive task even when the target
    # account has no visible shell.  Prove the user's Explorer session first;
    # otherwise the wait for the task's exit marker can consume the full lane
    # timeout without ever starting the production window.
    _preflight_interactive_desktop(
        run_dir=run_dir, logs=logs, timeout=timeout, user=user,
    )
    for path in (pid, exit_code, stdout, stderr):
        try:
            path.unlink()
        except FileNotFoundError:
            pass
        except OSError as error:
            raise LocalVMError(f"Windows native UI marker is not removable: {path.name}") from error
    wrapper.write_text(
        _native_ui_wrapper(
            command,
            cwd=cwd,
            environment=filtered_environment,
            stdout=stdout,
            stderr=stderr,
            pid=pid,
            exit_code=exit_code,
        ),
        encoding="utf-8",
    )
    registered = False
    failure: Exception | None = None
    try:
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
                if not _PID.fullmatch(value):
                    raise LocalVMError("Windows native UI PID marker is invalid")
                kill_environment = os.environ.copy()
                kill_environment["DOBBYVPN_NATIVE_UI_PID"] = value
                _powershell(
                    _NATIVE_UI_KILL_SCRIPT,
                    cwd=run_dir,
                    logs=logs,
                    label="native-ui-kill",
                    timeout=min(timeout, 15.0),
                    environment=kill_environment,
                )
            raise LocalVMError("Windows native UI task timed out")
        try:
            raw_exit_code = exit_code.read_text(encoding="ascii").strip()
        except OSError as error:
            raise LocalVMError("Windows native UI exit marker is unreadable") from error
        if not re.fullmatch(r"-?[0-9]+", raw_exit_code):
            raise LocalVMError("Windows native UI exit marker is invalid")
        returncode = int(raw_exit_code)
        return subprocess.CompletedProcess(
            command,
            returncode,
            stdout.read_bytes() if stdout.is_file() else b"",
            stderr.read_bytes() if stderr.is_file() else b"",
        )
    except Exception as error:
        failure = error
        raise
    finally:
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
                if failure is None:
                    raise LocalVMError(
                        f"Windows native UI task cleanup failed: {cleanup_error}"
                    ) from cleanup_error
                # Preserve the original UI error while retaining cleanup
                # diagnostics in the command log produced by _powershell.
                raise LocalVMError(
                    f"{failure}; Windows native UI task cleanup failed: {cleanup_error}"
                ) from failure
        for path in (wrapper, pid, exit_code):
            try:
                path.unlink()
            except FileNotFoundError:
                pass
            except OSError:
                # These are disposable markers.  The task cleanup result and
                # UI output remain authoritative diagnostics.
                pass


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
    token_user = environment.get("DOBBYVPN_CONTROL_TOKEN_USER", "").strip()
    if not token_user or token_user.upper() in {"SYSTEM", "NT AUTHORITY\\SYSTEM"}:
        raise _error("Windows control-token user is not configured for SYSTEM session")
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
$record = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue) -ErrorAction Stop
if ($null -eq $record) { exit 0 }
if ($null -eq $record.CreationDate) { throw "service creation time unavailable" }
$observed = [string]$record.ProcessId + "|" + [string]$record.CreationDate.ToUniversalTime().Ticks
if ($observed -ne $expected) { exit 0 }
if ([IO.Path]::GetFullPath([string]$record.ExecutablePath) -ine $expectedPath) { exit 0 }
$owner = Invoke-CimMethod -InputObject $record -MethodName GetOwner -ErrorAction Stop
if ($owner.ReturnValue -ne 0) { throw "service owner unavailable" }
if ([string]$owner.User -cne "SYSTEM" -or [string]$owner.Domain -cne "NT AUTHORITY") { exit 0 }
& "$env:SystemRoot\System32\taskkill.exe" /PID $pidValue /T /F | Out-Null
if ($LASTEXITCODE -ne 0) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue) -ErrorAction SilentlyContinue
  if ($null -ne $still) { throw "service taskkill failed" }
}
$deadline = [DateTime]::UtcNow.AddSeconds(15)
while ([DateTime]::UtcNow -lt $deadline) {
  $still = Get-CimInstance Win32_Process -Filter ("ProcessId = {0}" -f $pidValue) -ErrorAction SilentlyContinue
  if ($null -eq $still) { exit 0 }
  Start-Sleep -Milliseconds 100
}
throw "service did not terminate"
'''


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
$adapters | Enable-NetAdapter -Confirm:$false -ErrorAction Stop | Out-Null
$adapter = Get-NetAdapter -InterfaceIndex $index -ErrorAction Stop
if ($adapter.Status -ne "Up") { throw "recorded network adapter is not up" }
'''


def cleanup(run_dir: Path, runtime: dict[str, Any], logs: Path, timeout: float) -> None:
    """Stop only the recorded SYSTEM process and remove the exact test rule."""

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
            env = os.environ.copy()
            env.update({
                "DOBBYVPN_SERVICE_PID": str(pid),
                "DOBBYVPN_SERVICE_IDENTITY": identity,
                "DOBBYVPN_SERVICE_BINARY": str(binary.resolve()),
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
    if errors:
        raise _error("; ".join(errors))
