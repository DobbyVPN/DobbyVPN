#!/usr/bin/env python3
"""Qualify desktop installer migration and rollback transitions.

The current package is supplied by the Release workflow.  The previous
published package is downloaded into a temporary directory and verified
against ``installer_rollback_manifest.json`` before it is used.  The temporary
directory is removed on every outcome, so a migration run never leaves a
rollback package on the runner.

This module deliberately keeps package-manager commands in two small adapters.
The transition sequence is shared and unit-testable without Windows or macOS.
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from typing import Callable, Mapping, Sequence
from urllib.request import Request, urlopen


SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_MANIFEST = SCRIPT_DIR / "installer_rollback_manifest.json"
CURRENT_VERSION = "1.5.1"
OLD_VERSION = "1.5.0"
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
PLATFORMS = ("windows", "macos")
MACOS_ASSET = {"arm64": "macos-aarch64", "x86_64": "macos-amd64"}


class MigrationError(RuntimeError):
    """A package migration precondition or transition failed."""


def _render_stream(value: object) -> str:
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="backslashreplace")
    return str(value)


def _stream_bytes(value: object) -> bytes:
    if value is None:
        return b""
    if isinstance(value, bytes):
        return value
    return str(value).encode("utf-8", errors="surrogatepass")


def _emit_command_streams(label: str, stdout: object, stderr: object) -> None:
    """Forward complete command streams while retaining current-run files."""

    for name, value in (("stdout", stdout), ("stderr", stderr)):
        rendered = _render_stream(value)
        print(f"[installer {label} {name} begin]", file=sys.stderr)
        print(rendered, end="" if rendered.endswith("\n") else "\n", file=sys.stderr)
        print(f"[installer {label} {name} end]", file=sys.stderr)


@dataclass(frozen=True)
class RollbackAsset:
    platform: str
    name: str
    url: str
    sha256: str
    size: int


def _error(message: str) -> MigrationError:
    return MigrationError(message)


def load_manifest(path: Path = DEFAULT_MANIFEST) -> dict[str, RollbackAsset]:
    """Load and strictly validate the checked-in v1.5.0 asset manifest."""
    try:
        payload = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise _error(f"rollback manifest is unreadable: {path}") from error
    if not isinstance(payload, dict) or set(payload) != {"schema", "tag", "version", "assets"}:
        raise _error("rollback manifest has an unexpected schema")
    if payload["schema"] != 1 or payload["tag"] != "v1.5.0" or payload["version"] != OLD_VERSION:
        raise _error("rollback manifest must describe exactly v1.5.0")
    records = payload["assets"]
    if not isinstance(records, dict) or set(records) != {"windows", "macos-aarch64", "macos-amd64"}:
        raise _error("rollback manifest must contain Windows and both macOS assets")

    result: dict[str, RollbackAsset] = {}
    for platform, raw in records.items():
        if not isinstance(raw, dict) or set(raw) != {"name", "url", "sha256", "size"}:
            raise _error(f"rollback manifest asset {platform!r} has an unexpected schema")
        name = raw["name"]
        url = raw["url"]
        digest = raw["sha256"]
        size = raw["size"]
        if not all(isinstance(value, str) for value in (name, url, digest)):
            raise _error(f"rollback manifest asset {platform!r} has invalid text")
        if not SHA256_RE.fullmatch(digest):
            raise _error(f"rollback manifest asset {platform!r} has an invalid SHA-256")
        if not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise _error(f"rollback manifest asset {platform!r} has an invalid size")
        expected_prefix = "https://github.com/DobbyVPN/DobbyVPN/releases/download/v1.5.0/"
        if url != f"{expected_prefix}{name}":
            raise _error(f"rollback manifest asset {platform!r} URL is not the v1.5.0 release asset")
        if platform == "windows" and name != "dobbyVPN-windows-amd64.msi":
            raise _error("Windows rollback asset name is invalid")
        if platform == "macos-aarch64" and name != "dobbyVPN-macos-aarch64.pkg":
            raise _error("macOS arm64 rollback asset name is invalid")
        if platform == "macos-amd64" and name != "dobbyVPN-macos-amd64.pkg":
            raise _error("macOS Intel rollback asset name is invalid")
        result[platform] = RollbackAsset(platform, name, url, digest, size)
    return result


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with Path(path).open("rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
    except OSError as error:
        raise _error(f"cannot read package for hashing: {path}") from error
    return digest.hexdigest()


def verify_asset(path: Path, asset: RollbackAsset) -> None:
    """Reject a downloaded package unless both size and digest are pinned."""
    try:
        actual_size = Path(path).stat().st_size
    except OSError as error:
        raise _error(f"rollback package is missing: {path}") from error
    if actual_size != asset.size:
        raise _error(f"{asset.name} size {actual_size} does not match pinned {asset.size}")
    actual_digest = sha256_file(path)
    if actual_digest != asset.sha256:
        raise _error(f"{asset.name} SHA-256 does not match the pinned v1.5.0 value")


def download_asset(
    asset: RollbackAsset,
    destination: Path,
    *,
    opener: Callable[..., object] = urlopen,
    timeout: float = 60.0,
) -> Path:
    """Download one asset to a temporary file and verify it before publishing it."""
    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    partial = destination.with_name(f".{destination.name}.partial")
    partial.unlink(missing_ok=True)
    request = Request(asset.url, headers={"User-Agent": "DobbyVPN-release-migration"})
    try:
        with opener(request, timeout=timeout) as response, partial.open("wb") as output:
            shutil.copyfileobj(response, output, length=1024 * 1024)
        verify_asset(partial, asset)
        partial.replace(destination)
    except MigrationError:
        partial.unlink(missing_ok=True)
        raise
    except (OSError, ValueError) as error:
        partial.unlink(missing_ok=True)
        raise _error(f"download of {asset.name} failed: {error}") from error
    return destination


@dataclass
class CommandRunner:
    """Native command runner kept injectable for migration unit tests."""

    log_dir: Path

    def run(
        self,
        command: Sequence[str],
        *,
        label: str,
        accepted_codes: frozenset[int] = frozenset({0}),
        environment: Mapping[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        self.log_dir.mkdir(parents=True, exist_ok=True)
        stdout_path = self.log_dir / f"{label}.stdout.log"
        stderr_path = self.log_dir / f"{label}.stderr.log"
        try:
            completed = subprocess.run(
                list(command),
                check=False,
                capture_output=True,
                text=False,
                env=dict(environment) if environment is not None else None,
                timeout=300,
            )
        except subprocess.TimeoutExpired as error:
            stdout = _stream_bytes(getattr(error, "stdout", None))
            stderr = _stream_bytes(getattr(error, "stderr", None))
            _emit_command_streams(
                label, stdout, stderr,
            )
            stdout_path.write_bytes(stdout)
            stderr_path.write_bytes(stderr)
            raise _error(f"{label} failed to execute: {error}") from error
        except OSError as error:
            raise _error(f"{label} failed to execute: {error}") from error
        _emit_command_streams(label, completed.stdout, completed.stderr)
        stdout = _stream_bytes(completed.stdout)
        stderr = _stream_bytes(completed.stderr)
        stdout_path.write_bytes(stdout)
        stderr_path.write_bytes(stderr)
        if completed.returncode not in accepted_codes:
            raise _error(f"{label} exited with code {completed.returncode}")
        return subprocess.CompletedProcess(
            completed.args,
            completed.returncode,
            _render_stream(stdout),
            _render_stream(stderr),
        )


class InstallerAdapter:
    """Interface implemented by native package-manager adapters."""

    def install(self, package: Path, *, label: str) -> None:
        raise NotImplementedError

    def uninstall(self, package: Path, *, label: str, allow_missing: bool = False) -> None:
        raise NotImplementedError

    def verify_installed(self, expected_version: str, *, label: str) -> None:
        raise NotImplementedError

    def verify_uninstalled(self, *, label: str) -> None:
        raise NotImplementedError


class WindowsInstaller(InstallerAdapter):
    """MSI install/uninstall and ARP/file checks for a Windows runner."""

    def __init__(self, runner: CommandRunner) -> None:
        self.runner = runner

    def install(self, package: Path, *, label: str) -> None:
        self.runner.run(
            [
                "msiexec.exe", "/i", str(package), "/qn", "/norestart",
                "/L*v", str(self.runner.log_dir / f"{label}.msi.log"),
            ],
            label=label,
            accepted_codes=frozenset({0, 3010}),
        )

    def uninstall(self, package: Path, *, label: str, allow_missing: bool = False) -> None:
        accepted = frozenset({0, 3010, 1605, 1614}) if allow_missing else frozenset({0, 3010})
        self.runner.run(
            [
                "msiexec.exe", "/x", str(package), "/qn", "/norestart",
                "/L*v", str(self.runner.log_dir / f"{label}.msi.log"),
            ],
            label=label,
            accepted_codes=accepted,
        )

    def verify_installed(self, expected_version: str, *, label: str) -> None:
        script = r'''
$ErrorActionPreference = "Stop"
function Get-DobbyArpEntries {
  foreach ($path in @(
    'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
    'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
  )) {
    try {
      Get-ItemProperty -Path $path -ErrorAction Stop
    } catch {
      $detail = $_ | Out-String
      if ($detail -match 'Cannot find path|does not exist|cannot find') {
        [Console]::Error.WriteLine($detail)
        continue
      }
      throw
    }
  }
}
$entries = @(
  @(Get-DobbyArpEntries) | Where-Object { $_.DisplayName -eq 'DobbyVPN' }
)
if ($entries.Count -ne 1) { throw "expected one DobbyVPN ARP entry" }
if ([string]$entries[0].DisplayVersion -ne $env:DOBBYVPN_EXPECTED_VERSION) { throw "unexpected installed version" }
$root = Join-Path ${env:ProgramFiles} 'DobbyVPN'
foreach ($name in @('bin\DobbyVPN.exe', 'bin\dobby-cli.exe', 'bin\dobbyvpn-backend.exe')) {
  if (-not (Test-Path (Join-Path $root $name) -PathType Leaf)) { throw "missing installed file $name" }
}
$service = Get-Service -Name 'DobbyVPN Go backend' -ErrorAction Stop
if ($service.Status -ne 'Running') { throw "DobbyVPN Go backend is not running" }
'''
        environment = {**os.environ, "DOBBYVPN_EXPECTED_VERSION": expected_version}
        self.runner.run(["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script], label=label, environment=environment)

    def verify_uninstalled(self, *, label: str) -> None:
        script = r'''
$ErrorActionPreference = "Stop"
function Get-DobbyArpEntries {
  foreach ($path in @(
    'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
    'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
  )) {
    try {
      Get-ItemProperty -Path $path -ErrorAction Stop
    } catch {
      $detail = $_ | Out-String
      if ($detail -match 'Cannot find path|does not exist|cannot find') {
        [Console]::Error.WriteLine($detail)
        continue
      }
      throw
    }
  }
}
$entries = @(
  @(Get-DobbyArpEntries) | Where-Object { $_.DisplayName -eq 'DobbyVPN' }
)
if ($entries.Count -ne 0) { throw "DobbyVPN remains registered after uninstall" }
if (Test-Path (Join-Path ${env:ProgramFiles} 'DobbyVPN')) { throw "DobbyVPN install directory remains" }
$service = $null
try {
  $service = Get-Service -Name 'DobbyVPN Go backend' -ErrorAction Stop
} catch {
  $detail = $_ | Out-String
  if ($detail -match 'Cannot find any service|cannot find|does not exist') {
    [Console]::Error.WriteLine($detail)
  } else {
    throw
  }
}
if ($null -ne $service) { throw "DobbyVPN Go backend remains registered" }
'''
        self.runner.run(["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script], label=label)


class MacOSInstaller(InstallerAdapter):
    """PKG install and the product-owned uninstall path on macOS."""

    def __init__(self, runner: CommandRunner, *, fallback_uninstaller: Path) -> None:
        self.runner = runner
        self.fallback_uninstaller = fallback_uninstaller

    def install(self, package: Path, *, label: str) -> None:
        environment = {**os.environ, "DOBBYVPN_CONTROL_PEER_UID": str(os.getuid())}
        self.runner.run(
            ["sudo", "-n", "env", f"DOBBYVPN_CONTROL_PEER_UID={environment['DOBBYVPN_CONTROL_PEER_UID']}", "installer", "-pkg", str(package), "-target", "/"],
            label=label,
            environment=environment,
        )

    def uninstall(self, package: Path, *, label: str, allow_missing: bool = False) -> None:
        uninstaller = Path("/usr/local/libexec/dobbyvpn-uninstall")
        if uninstaller.is_file():
            selected = uninstaller
        elif self.fallback_uninstaller.is_file():
            # v1.5.0 predates the installed helper. The checked-out current
            # script is the same fixed-path product-owned implementation and
            # is safe to use for cleanup before or after the old package.
            selected = self.fallback_uninstaller
        elif allow_missing:
            return
        else:
            raise _error("macOS product uninstaller is unavailable")
        self.runner.run(["sudo", "-n", str(selected)], label=label)

    def verify_installed(self, expected_version: str, *, label: str) -> None:
        script = r'''
set -eu
package_info="$(mktemp -t dobbyvpn-pkg-info.XXXXXX)"
trap 'rm -f "$package_info"' EXIT
set +e
pkgutil --pkg-info com.dobby.pkg >"$package_info" 2>&1
package_status=$?
set -e
cat "$package_info"
test "$package_status" -eq 0
version="$(awk '/^version:/{print $2}' "$package_info")"
test "$version" = "$EXPECTED_VERSION"
test -x "/Applications/Dobby VPN.app/Contents/Resources/dobbyvpn-backend"
test -x "/Applications/Dobby VPN.app/Contents/Resources/dobby-cli"
test -f "/Library/LaunchDaemons/com.dobby.vpnservice.plist"
if [ "$EXPECTED_VERSION" = "1.5.1" ]; then
  test -x "/usr/local/libexec/dobbyvpn-uninstall"
fi
launchctl print system/com.dobby.vpnservice
'''
        self.runner.run(["/bin/sh", "-c", script], label=label, environment={**os.environ, "EXPECTED_VERSION": expected_version})

    def verify_uninstalled(self, *, label: str) -> None:
        script = r'''
set -eu
set +e
package_output="$(pkgutil --pkg-info com.dobby.pkg 2>&1)"
package_status=$?
set -e
printf '%s\n' "$package_output"
if [ "$package_status" -eq 0 ]; then
  echo "DobbyVPN package receipt remains" >&2
  exit 1
fi
test ! -e "/Applications/Dobby VPN.app"
test ! -e "/Library/LaunchDaemons/com.dobby.vpnservice.plist"
test ! -e "/var/run/dobbyvpn/control.sock"
test ! -e "/usr/local/libexec/dobbyvpn-uninstall"
set +e
launchd_output="$(launchctl print system/com.dobby.vpnservice 2>&1)"
launchd_status=$?
set -e
printf '%s\n' "$launchd_output"
if [ "$launchd_status" -eq 0 ]; then
  echo "DobbyVPN launchd service remains" >&2
  exit 1
fi
'''
        self.runner.run(["/bin/sh", "-c", script], label=label)


def migration_sequence(adapter: InstallerAdapter, current: Path, previous: Path, *, current_version: str = CURRENT_VERSION) -> None:
    """Run fresh-install, upgrade, rollback, and final-uninstall transitions."""
    adapter.uninstall(current, label="baseline-uninstall", allow_missing=True)
    adapter.verify_uninstalled(label="baseline-empty")

    adapter.install(current, label="fresh-install")
    adapter.verify_installed(current_version, label="fresh-installed")
    adapter.uninstall(current, label="fresh-uninstall")
    adapter.verify_uninstalled(label="fresh-uninstalled")

    adapter.install(previous, label="old-install")
    adapter.verify_installed(OLD_VERSION, label="old-installed")
    adapter.install(current, label="upgrade-install")
    adapter.verify_installed(current_version, label="upgraded-to-current")

    # Rollback is intentionally an uninstall followed by a clean old install;
    # it must not rely on a package-manager downgrade or mutable cache.
    adapter.uninstall(current, label="rollback-uninstall-current")
    adapter.verify_uninstalled(label="rollback-empty")
    adapter.install(previous, label="rollback-install-old")
    adapter.verify_installed(OLD_VERSION, label="rolled-back-to-old")

    adapter.uninstall(previous, label="final-uninstall")
    adapter.verify_uninstalled(label="final-empty")


def qualify(
    platform: str,
    current_package: Path,
    *,
    current_version: str = CURRENT_VERSION,
    manifest_path: Path = DEFAULT_MANIFEST,
    log_dir: Path | None = None,
) -> None:
    if platform not in PLATFORMS:
        raise _error(f"unsupported migration platform: {platform}")
    current_package = Path(current_package)
    if not current_package.is_file():
        raise _error(f"current release package is missing: {current_package}")
    manifest = load_manifest(manifest_path)
    asset_key = platform
    if platform == "macos":
        import os

        try:
            asset_key = MACOS_ASSET[os.uname().machine]
        except KeyError as error:
            raise _error(f"unsupported macOS architecture: {os.uname().machine}") from error
    asset = manifest[asset_key]
    run_root = Path(tempfile.mkdtemp(prefix="dobbyvpn-installer-migration-"))
    original_error: BaseException | None = None
    adapter: InstallerAdapter | None = None
    previous_package: Path | None = None
    try:
        previous_package = download_asset(asset, run_root / asset.name)
        runner = CommandRunner(Path(log_dir) if log_dir is not None else run_root / "logs")
        if platform == "windows":
            adapter = WindowsInstaller(runner)
        else:
            adapter = MacOSInstaller(
                runner, fallback_uninstaller=SCRIPT_DIR.parent.parent / "installer/macos/uninstall.sh"
            )
        assert adapter is not None
        migration_sequence(
            adapter, current_package, previous_package, current_version=current_version
        )
    except BaseException as error:
        original_error = error
        if adapter is not None and previous_package is not None:
            # A failed transition must not leave a package installed on a
            # reusable runner. Preserve the transition error while reporting
            # any cleanup failure separately.
            for package, label in (
                (current_package, "failure-uninstall-current"),
                (previous_package, "failure-uninstall-previous"),
            ):
                try:
                    adapter.uninstall(package, label=label, allow_missing=True)
                except BaseException as cleanup_error:
                    print(
                        f"installer migration cleanup failed: {label}: {cleanup_error}",
                        file=sys.stderr,
                    )
            try:
                adapter.verify_uninstalled(label="failure-empty")
            except BaseException as cleanup_error:
                print(
                    f"installer migration cleanup failed: failure-empty: {cleanup_error}",
                    file=sys.stderr,
                )
        raise
    finally:
        try:
            shutil.rmtree(run_root)
        except OSError as cleanup_error:
            message = f"cannot remove temporary migration directory {run_root}: {cleanup_error}"
            if original_error is None:
                raise _error(message) from cleanup_error
            print(f"installer migration cleanup failed: {message}", file=sys.stderr)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=PLATFORMS, required=True)
    parser.add_argument("--package", type=Path, required=True, help="exact 1.5.1 package from this Release run")
    parser.add_argument("--current-version", default=CURRENT_VERSION)
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--log-dir", type=Path, help="optional diagnostic log directory")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        qualify(
            args.platform,
            args.package,
            current_version=args.current_version,
            manifest_path=args.manifest,
            log_dir=args.log_dir,
        )
    except MigrationError as error:
        print(f"installer migration failed: {error}", file=sys.stderr)
        return 1
    print(f"installer migration passed: {args.platform} {args.current_version} fresh/upgrade/rollback/uninstall")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
