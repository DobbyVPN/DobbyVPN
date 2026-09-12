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

_PID = re.compile(r"^[1-9][0-9]*$")
_IDENTITY = re.compile(r"^[1-9][0-9]*\|[1-9][0-9]+$")
_INTERFACE = re.compile(r"^[1-9][0-9]*$")
_CONTROL_ADDRESS = "127.0.0.1:50051"
_FIREWALL_RULE = "DobbyVPN-Torturer-Routing-Probe"


def _error(message: str) -> Exception:
    # Keep this module importable while local_vm imports the platform modules.
    from .local_vm import LocalVMError

    return LocalVMError(message)


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
