#!/usr/bin/env python3
"""Make the install of the bundled runtime files independent of the versions already installed.

Windows Installer decides, while costing the new package, whether a file or component is installed
by comparing the File table Version with the version of the file on disk (the "Disallowing
installation of component ... higher versioned keyfile exists" rule and the file versioning rule).
A major upgrade removes the old product only after that decision, so a bundled file that is older
than the installed one is skipped and then deleted with the old product.

The files harvested from bin\\** are therefore written with the highest possible File table Version.
Costing then always installs them, whatever runtime the installed product bundled. The real version of
the bundled runtime is recorded in the Property table so that the release checks can still read it.
The edit is made after `wix build`, so the WiX ICE validation (which compares the table with the file)
has already run.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

FORCED_VERSION = "65535.65535.65535.65535"
# WiX names the files and components it harvests with <Files> "fls...", explicit <File> elements "fil...".
# Explicit components (DobbyVPN.exe and the service) keep their real versions.
HARVESTED_PREFIX = "fls"
RUNTIME_PROPERTY = "DobbyVPNBundledRuntime"
MINIMUM_OVERRIDDEN = 100  # the bin\** harvest has several hundred files; fewer means the naming changed

SCRIPT = r'''
$ErrorActionPreference = "Stop"
$msi = (Resolve-Path $env:DOBBYVPN_MSI).Path
$installer = New-Object -ComObject WindowsInstaller.Installer
$database = $installer.GetType().InvokeMember("OpenDatabase", "InvokeMethod", $null, $installer, @($msi, 1))
function Open-View($sql) {
  $view = $database.GetType().InvokeMember("OpenView", "InvokeMethod", $null, $database, $sql)
  $view.GetType().InvokeMember("Execute", "InvokeMethod", $null, $view, $null)
  return $view
}
function Fetch($view) { return $view.GetType().InvokeMember("Fetch", "InvokeMethod", $null, $view, $null) }
function Field($record, $index) { return $record.GetType().InvokeMember("StringData", "GetProperty", $null, $record, $index) }
$existing = Open-View ("SELECT `Value` FROM `Property` WHERE `Property` = '" + $env:DOBBYVPN_PROPERTY + "'")
if ($null -ne (Fetch $existing)) { Write-Output "ALREADY_FORCED=1"; exit 0 }
$files = Open-View 'SELECT `File`, `Component_`, `Version`, `FileName` FROM `File`'
$ids = New-Object System.Collections.Generic.List[string]
$runtime = $null
while ($true) {
  $record = Fetch $files
  if ($null -eq $record) { break }
  $id = Field $record 1; $component = Field $record 2; $version = Field $record 3; $name = (Field $record 4) -split '\|' | Select-Object -Last 1
  if ($name -ieq 'coreclr.dll') { $runtime = $version }
  if ($component.StartsWith($env:DOBBYVPN_PREFIX) -and $version -ne '') { $ids.Add($id) }
}
if ($null -eq $runtime) { throw "the MSI has no coreclr.dll" }
foreach ($id in $ids) {
  $null = Open-View ("UPDATE `File` SET `Version` = '" + $env:DOBBYVPN_VERSION + "' WHERE `File` = '" + $id.Replace("'", "''") + "'")
}
$null = Open-View ("INSERT INTO `Property` (`Property`, `Value`) VALUES ('" + $env:DOBBYVPN_PROPERTY + "', '" + $runtime + "')")
$database.GetType().InvokeMember("Commit", "InvokeMethod", $null, $database, $null)
Write-Output "OVERRIDDEN=$($ids.Count)"
Write-Output "BUNDLED_RUNTIME=$runtime"
'''


class MsiRuntimeError(RuntimeError):
    """The MSI could not be edited."""


def force_harvested_versions(msi: Path, env: dict[str, str] | None = None, *, run=subprocess.run) -> tuple[int, str]:
    """Edit the MSI in place; return (files changed, the real bundled coreclr.dll version). A second call changes nothing."""
    environment = dict(env) if env is not None else {}
    environment.update(
        DOBBYVPN_MSI=str(msi), DOBBYVPN_VERSION=FORCED_VERSION, DOBBYVPN_PREFIX=HARVESTED_PREFIX,
        DOBBYVPN_PROPERTY=RUNTIME_PROPERTY,
    )
    import os
    merged = {**os.environ, **environment}
    completed = run(
        ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", SCRIPT],
        env=merged, stdin=subprocess.DEVNULL, capture_output=True, check=False, timeout=300,
    )
    text = (completed.stdout or b"").decode("utf-8", errors="replace")
    if completed.returncode != 0:
        raise MsiRuntimeError(f"editing {Path(msi).name} failed with exit {completed.returncode}: {(completed.stderr or b'').decode('utf-8', 'replace')[-500:]}")
    if "ALREADY_FORCED=1" in text:
        return 0, ""
    count = re.search(r"^OVERRIDDEN=(\d+)\s*$", text, re.MULTILINE)
    runtime = re.search(r"^BUNDLED_RUNTIME=(\S+)\s*$", text, re.MULTILINE)
    if count is None or runtime is None:
        raise MsiRuntimeError(f"editing {Path(msi).name} reported no result")
    if int(count.group(1)) < MINIMUM_OVERRIDDEN:
        raise MsiRuntimeError(f"only {count.group(1)} harvested files were found in {Path(msi).name}; the WiX naming of harvested components changed")
    return int(count.group(1)), runtime.group(1)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    apply = commands.add_parser("apply", help="force the harvested files' versions in the MSI")
    apply.add_argument("--msi", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        count, runtime = force_harvested_versions(args.msi)
    except MsiRuntimeError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    print(f"{args.msi.name}: already forced" if count == 0 else f"{args.msi.name}: forced {count} files; bundled runtime {runtime}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
