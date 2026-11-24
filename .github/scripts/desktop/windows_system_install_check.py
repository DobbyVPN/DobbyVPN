#!/usr/bin/env python3
"""Check the SYSTEM install guard of a built MSI on a Windows runner (a debug-packages step).

Intune and SCCM install under NT AUTHORITY\\SYSTEM. Without DOBBYVPN_CONTROL_PIPE_SID the backend refuses that identity, so the MSI
must stop in its launch condition, before anything is installed, with an actionable message; with the property it must install
and run; an uninstall must not need it. The checks run the exact MSI that was just built, through a one-off scheduled task that
runs as SYSTEM, and clean up in `finally`.
"""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import subprocess
import sys
import time

MSI_FAILED = 1603
MESSAGE = "cannot be installed or upgraded under the SYSTEM account"
SERVICE = "DobbyVPN Go backend"
POWERSHELL = ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command"]

TASK = r'''
$ErrorActionPreference = "Stop"
$name = "DobbyVPN-system-check-" + [guid]::NewGuid().ToString("N")
$action = New-ScheduledTaskAction -Execute "msiexec.exe" -Argument $env:DOBBYVPN_MSIEXEC_ARGUMENTS
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName $name -Action $action -Principal $principal | Out-Null
try {
  Start-ScheduledTask -TaskName $name
  $deadline = (Get-Date).AddMinutes(8)
  do {
    Start-Sleep -Seconds 2
    $state = (Get-ScheduledTask -TaskName $name).State
    $info = Get-ScheduledTaskInfo -TaskName $name
  } while (($state -eq 'Running' -or $info.LastTaskResult -eq 267011) -and (Get-Date) -lt $deadline)
  if ($state -eq 'Running' -or $info.LastTaskResult -eq 267011) { throw "msiexec under SYSTEM did not finish within 8 minutes" }
  Write-Output ("TASK_RESULT=" + $info.LastTaskResult)
} finally {
  Unregister-ScheduledTask -TaskName $name -Confirm:$false -ErrorAction SilentlyContinue
}
'''
STATE = r'''
$service = Get-Service -Name $env:DOBBYVPN_SERVICE -ErrorAction SilentlyContinue
Write-Output ("SERVICE=" + $(if ($service) { $service.Status } else { 'absent' }))
Write-Output ("FILES=" + (Test-Path (Join-Path $env:ProgramFiles 'DobbyVPN\bin')))
'''
SID = "[Security.Principal.WindowsIdentity]::GetCurrent().User.Value"


class CheckError(RuntimeError):
    """The MSI does not behave as the SYSTEM install guard requires."""


def _powershell(script: str, environment: dict[str, str] | None = None, *, run=subprocess.run) -> str:
    completed = run([*POWERSHELL, script], capture_output=True, check=False, timeout=600, stdin=subprocess.DEVNULL,
                    env={**os.environ, **(environment or {})})
    output = (completed.stdout or b"").decode("utf-8", errors="replace")
    if completed.returncode != 0:
        raise CheckError(f"PowerShell exited {completed.returncode}: {(completed.stderr or b'').decode('utf-8', 'replace')[-400:]}")
    return output


def msiexec_as_system(package: Path, log: Path, *, action: str = "/i", properties: tuple[str, ...] = (), run=subprocess.run) -> int:
    arguments = " ".join([action, f'"{package}"', "/qn", "/norestart", *properties, "/L*v", f'"{log}"'])
    output = _powershell(TASK, {"DOBBYVPN_MSIEXEC_ARGUMENTS": arguments}, run=run)
    found = re.findall(r"^TASK_RESULT=(-?\d+)\s*$", output, re.MULTILINE)
    if len(found) != 1:
        raise CheckError("the SYSTEM task reported no msiexec result")
    return int(found[0]) & 0xFFFFFFFF


def product_state(*, run=subprocess.run) -> tuple[str, bool]:
    output = _powershell(STATE, {"DOBBYVPN_SERVICE": SERVICE}, run=run)
    service = re.search(r"^SERVICE=(\S+)", output, re.MULTILINE)
    files = re.search(r"^FILES=(\S+)", output, re.MULTILINE)
    if service is None or files is None:
        raise CheckError("the product state could not be read")
    return service.group(1), files.group(1) == "True"


def check(package: Path, log_dir: Path, *, run=subprocess.run, clock=time.monotonic) -> list[str]:
    """Run the three checks; return the timing lines. Always tries to remove what it installed."""
    log_dir.mkdir(parents=True, exist_ok=True)
    timings: list[str] = []
    installed = False

    def timed(label: str, body):
        started = clock()
        try:
            return body()
        finally:
            timings.append(f"{label}: {clock() - started:.0f} s")

    def read(name: str) -> str:
        return (log_dir / name).read_text(encoding="utf-8", errors="replace")

    sid = _powershell(SID, run=run).strip().splitlines()[-1].strip()
    if not re.fullmatch(r"S-1-(?:\d+-)*\d+", sid) or sid == "S-1-5-18":
        raise CheckError(f"unexpected runner account SID: {sid!r}")
    installed = True  # until the checks show nothing was installed: the cleanup below then covers a guard that does not hold
    try:
        # 1. Without the property: refused early, nothing installed.
        code = timed("refused without the property", lambda: msiexec_as_system(package, log_dir / "no-sid.log", run=run))
        log = read("no-sid.log")
        if code != MSI_FAILED or MESSAGE not in log or "DOBBYVPN_CONTROL_PIPE_SID" not in log:
            raise CheckError(f"without the property msiexec exited {code}; expected {MSI_FAILED} with the launch condition message")
        if "Error 1920" in log or re.search(r"Action start \d+:\d+:\d+: InstallFiles", log):
            raise CheckError("the install got past the launch condition (files or the service were reached)")
        if product_state(run=run) != ("absent", False):
            raise CheckError("the refused install left files or the service behind")
        # 2. With the runner account's SID: installs and the service runs.
        code = timed("install with the property", lambda: msiexec_as_system(
            package, log_dir / "with-sid.log", properties=(f"DOBBYVPN_CONTROL_PIPE_SID={sid}",), run=run))
        if code not in (0, 3010) or product_state(run=run) != ("Running", True):
            raise CheckError(f"with the property msiexec exited {code}, state {product_state(run=run)}")
        # 3. Uninstall under SYSTEM needs no property.
        code = timed("uninstall without the property", lambda: msiexec_as_system(package, log_dir / "uninstall.log", action="/x", run=run))
        installed = False
        if code not in (0, 3010) or product_state(run=run) != ("absent", False):
            raise CheckError(f"uninstall under SYSTEM exited {code}, state {product_state(run=run)}")
    finally:
        if installed:
            try:
                msiexec_as_system(package, log_dir / "cleanup.log", action="/x", run=run)
            except CheckError as error:
                print(f"cleanup failed: {error}", file=sys.stderr)
    return timings


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package", type=Path, required=True)
    parser.add_argument("--log-dir", type=Path, required=True)
    args = parser.parse_args(argv)
    started = time.monotonic()
    try:
        timings = check(args.package.resolve(), args.log_dir.resolve())
    except CheckError as error:
        print(f"SYSTEM install check failed: {error}", file=sys.stderr)
        for log in sorted(args.log_dir.glob("*.log")):
            print(f"--- {log.name} (tail) ---", file=sys.stderr)
            print(log.read_text(encoding="utf-8", errors="replace")[-3000:], file=sys.stderr)
        return 1
    print("SYSTEM install check passed: " + "; ".join(timings) + f"; total {time.monotonic() - started:.0f} s")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
