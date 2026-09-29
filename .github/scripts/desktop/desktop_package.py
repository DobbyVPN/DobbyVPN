#!/usr/bin/env python3
"""Build, install, and test one native desktop platform package."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform as host_platform
import re
import shutil
import subprocess
import sys
import tempfile
from typing import Any


ROOT = Path(__file__).resolve().parents[3]
SCRIPT_DIR = Path(__file__).resolve().parent
SERVICES = ROOT / "runtime" / "services"
VERSION_PATTERN = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
SHA_PATTERN = re.compile(r"^[0-9a-f]{40}$")
PLATFORMS = ("linux", "windows", "macos")
NATIVE_UI_REQUIREMENTS = SCRIPT_DIR.parent / "requirements-native-ui.txt"
WINDOWS_MSI_VERIFY_TIMEOUT_SECONDS = 180


class DesktopPlatformError(RuntimeError):
    """A desktop package build, install, or cleanup failure."""


def _fail(message: str) -> None:
    raise DesktopPlatformError(message)


def _log(message: str) -> None:
    print(f"[desktop] {message}", flush=True)


def _forward(stream: Any, payload: bytes) -> None:
    if not payload:
        return
    binary = getattr(stream, "buffer", None)
    if binary is not None:
        binary.write(payload)
        binary.flush()
    else:
        stream.write(payload.decode("utf-8", errors="backslashreplace"))
        stream.flush()


def _run(
    label: str,
    command: list[str],
    *,
    cwd: Path = ROOT,
    env: dict[str, str] | None = None,
    check: bool = True,
    capture: bool = False,
    timeout_seconds: float | None = None,
) -> subprocess.CompletedProcess[bytes]:
    _log(f"{label}: $ {' '.join(command)}")
    try:
        completed = subprocess.run(
            command,
            cwd=cwd,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
            check=False,
            timeout=timeout_seconds,
        )
    except subprocess.TimeoutExpired as error:
        # subprocess.run kills and reaps the timed-out process. Forward output
        # collected before termination so the timeout keeps its diagnostics.
        if capture:
            stdout = error.stdout
            stderr = error.stderr
            if isinstance(stdout, str):
                stdout = stdout.encode("utf-8", errors="backslashreplace")
            if isinstance(stderr, str):
                stderr = stderr.encode("utf-8", errors="backslashreplace")
            _forward(sys.stdout, stdout or b"")
            _forward(sys.stderr, stderr or b"")
        raise DesktopPlatformError(f"{label}: {error}") from error
    except OSError as error:
        _fail(f"{label}: command could not start: {error}")
    if capture:
        _forward(sys.stdout, completed.stdout or b"")
        _forward(sys.stderr, completed.stderr or b"")
    if check and completed.returncode != 0:
        _fail(f"{label}: command exited {completed.returncode}")
    return completed


def _emit_file(label: str, path: Path) -> None:
    print(f"[desktop {label} begin]", file=sys.stderr, flush=True)
    try:
        payload = path.read_bytes()
    except OSError as error:
        print(f"[desktop {label} collection error] {error}", file=sys.stderr, flush=True)
    else:
        _forward(sys.stderr, payload)
    print(f"[desktop {label} end]", file=sys.stderr, flush=True)


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.tmp")
    temporary.write_text(
        json.dumps(value, sort_keys=True, indent=2) + "\n", encoding="utf-8"
    )
    temporary.replace(path)


def _read_json(path: Path, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, ValueError) as error:
        raise DesktopPlatformError(f"{label} is unreadable: {path}") from error
    if not isinstance(value, dict):
        _fail(f"{label} must contain a JSON object")
    return value


def _host() -> tuple[str, str]:
    system = host_platform.system().lower()
    machine = host_platform.machine().lower()
    platform = {"darwin": "macos", "windows": "windows", "linux": "linux"}.get(system)
    architecture = "arm64" if machine in {"arm64", "aarch64"} else "amd64" if machine in {"amd64", "x86_64", "x64"} else ""
    if platform is None or not architecture:
        _fail(f"unsupported desktop build host: {system}/{machine}")
    return platform, architecture


def _source_sha(requested: str | None) -> str:
    if requested is not None and SHA_PATTERN.fullmatch(requested) is None:
        _fail("--source-sha must be a full lowercase 40-character commit SHA")
    if (ROOT / ".git").exists():
        result = _run(
            "source revision",
            ["git", "rev-parse", "HEAD"],
            check=False,
            capture=True,
        )
        observed = (result.stdout or b"").decode("ascii", errors="replace").strip()
        if result.returncode != 0 or SHA_PATTERN.fullmatch(observed) is None:
            _fail("could not read the full lowercase source commit SHA")
        if requested is not None and requested != observed:
            _fail(f"checked out {observed}, expected source commit {requested}")
        return requested or observed
    if requested is None:
        _fail("--source-sha is required when the source archive has no Git metadata")
    return requested


def _version(requested: str | None) -> str:
    value = requested or (ROOT / "VERSION").read_text(encoding="utf-8").strip()
    if VERSION_PATTERN.fullmatch(value) is None:
        _fail("version must be numeric x.y.z")
    return value


def _select_host(platform: str, architecture: str | None) -> tuple[str, str]:
    observed_platform, observed_arch = _host()
    selected_arch = architecture or observed_arch
    if platform != observed_platform:
        _fail(f"{platform} packages must be built on a {platform} runner")
    if selected_arch != observed_arch:
        _fail(
            f"{platform} {selected_arch} packages must be built on a native {selected_arch} runner; "
            f"this host is {observed_arch}"
        )
    if platform in {"linux", "windows"} and selected_arch != "amd64":
        _fail(f"{platform} packages are supported only for amd64")
    return platform, selected_arch


def _build(args: argparse.Namespace) -> int:
    platform, architecture = _select_host(args.platform, args.arch)
    version = _version(args.version)
    source_sha = _source_sha(args.source_sha)
    output = args.output_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)
    service_arch = architecture
    service_directory = SERVICES / f"{platform}-{service_arch}"
    environment = os.environ.copy()
    environment["VERSION_NAME"] = version
    environment["GITHUB_SHA"] = source_sha
    environment["APP_MAJOR_VERSION"], environment["APP_MINOR_VERSION"], environment["APP_MAINTENANCE_VERSION"] = version.split(".")

    desktop = SCRIPT_DIR / "desktop_build.py"
    _run(
        "Go backend and CLI build",
        [
            sys.executable,
            str(desktop),
            "libs",
            "--platform", platform,
            "--arch", architecture,
            "--with-cli",
            *( ["--skip-deps"] if args.skip_deps else [] ),
        ],
        env=environment,
    )
    if platform in {"windows", "macos"}:
        ui_output = service_directory / ("frontend" if platform == "windows" else "DobbyVPNMacApp")
        _run(
            "native desktop UI build",
            [
                sys.executable,
                str(desktop),
                "native-ui",
                "--platform", platform,
                "--arch", architecture,
                "--output", str(ui_output),
            ],
            env=environment,
        )

    with tempfile.TemporaryDirectory(prefix="dobbyvpn-desktop-package-") as temporary:
        work = Path(temporary)
        build_environment = (
            _ensure_windows_pillow(work, environment)
            if platform == "windows"
            else environment
        )
        archives = work / "archives"
        archives.mkdir()
        package_command = [
            sys.executable,
            str(SCRIPT_DIR / "package_desktop.py"),
            "--version", version,
            "--staging-root", str(SERVICES),
            "--output", str(archives),
            "--platform", platform,
        ]
        if platform == "macos":
            package_command.extend(("--arch", architecture))
        _run("desktop application payload assembly", package_command, env=build_environment)

        if platform == "linux":
            produced = archives / f"dobby-vpn_{version}_amd64.deb"
            _verify_deb(produced, version, work)
            target = output / "dobbyVPN-linux.deb"
        elif platform == "windows":
            archive = archives / f"dobby-vpn-{version}-windows-amd64.zip"
            produced = _build_windows_msi(archive, service_directory, version, source_sha, work, build_environment)
            target = output / "dobbyVPN-windows-amd64.msi"
        else:
            archive_arch = "aarch64" if architecture == "arm64" else "amd64"
            archive = archives / f"dobby-vpn-{version}-mac-{archive_arch}.zip"
            produced = _build_macos_pkg(archive, service_directory, architecture, version, source_sha, work, environment)
            target = output / f"dobbyVPN-macos-{archive_arch}.pkg"
        if not produced.is_file():
            _fail(f"package build did not produce {produced}")
        shutil.copyfile(produced, target)

    package_record = {
        "file_name": target.name,
        "path": str(target.resolve()),
        "sha256": _sha256(target),
    }
    descriptor = {
        "schema": 1,
        "mode": "desktop-package",
        "platform": platform,
        "architecture": architecture,
        "version": version,
        "source_sha": source_sha,
        "package": package_record,
        "package_path": package_record["path"],
        "package_sha256": package_record["sha256"],
    }
    _write_json(output / "desktop-package.json", descriptor)
    _log(f"built {package_record['file_name']} sha256={package_record['sha256']}")
    return 0


def _describe(args: argparse.Namespace) -> int:
    """Record identity for a package downloaded from the current Release run."""
    platform, architecture = _select_host(args.platform, args.arch)
    version = _version(args.version)
    source_sha = _source_sha(args.source_sha)
    package = args.package.resolve(strict=True)
    if not package.is_file():
        _fail(f"desktop package is not a regular file: {package}")

    if platform == "linux":
        expected_name = "dobbyVPN-linux.deb"
        if package.name != expected_name:
            _fail(f"Linux package must be named {expected_name}")
        package_version = _run(
            "read Linux package version",
            ["dpkg-deb", "-f", str(package), "Version"],
            capture=True,
        )
        architecture_result = _run(
            "read Linux package architecture",
            ["dpkg-deb", "-f", str(package), "Architecture"],
            capture=True,
        )
        observed_version = (package_version.stdout or b"").decode("utf-8", errors="replace").strip()
        observed_architecture = (architecture_result.stdout or b"").decode("utf-8", errors="replace").strip()
        if observed_version != version or observed_architecture != architecture:
            _fail(
                "Linux package metadata differs from the Release checkout: "
                f"version={observed_version!r}, architecture={observed_architecture!r}"
            )
        with tempfile.TemporaryDirectory(prefix="dobbyvpn-desktop-package-check-") as temporary:
            _verify_deb(package, version, Path(temporary))
    elif platform == "windows":
        expected_name = "dobbyVPN-windows-amd64.msi"
        if package.name != expected_name:
            _fail(f"Windows package must be named {expected_name}")
        _verify_msi(package, version, os.environ.copy())
    else:
        archive_arch = "aarch64" if architecture == "arm64" else "amd64"
        expected_name = f"dobbyVPN-macos-{archive_arch}.pkg"
        if package.name != expected_name:
            _fail(f"macOS package must be named {expected_name}")
        with tempfile.TemporaryDirectory(prefix="dobbyvpn-desktop-package-check-") as temporary:
            work = Path(temporary)
            _verify_pkg(package, version, work)
            _verify_macos_package_source(package, source_sha, architecture, work)

    package_record = {
        "file_name": package.name,
        "path": str(package),
        "sha256": _sha256(package),
    }
    descriptor = {
        "schema": 1,
        "mode": "desktop-package",
        "platform": platform,
        "architecture": architecture,
        "version": version,
        "source_sha": source_sha,
        "package": package_record,
        "package_path": package_record["path"],
        "package_sha256": package_record["sha256"],
    }
    _write_json(args.output, descriptor)
    _log(f"recorded {package.name} sha256={package_record['sha256']}")
    return 0


def _verify_macos_package_source(package: Path, source_sha: str, architecture: str, work: Path) -> None:
    expanded = work / "expanded-pkg"
    if not expanded.is_dir():
        _run("expand macOS package for source identity check", ["pkgutil", "--expand-full", str(package), str(expanded)])
    info_plists = list(expanded.rglob("Info.plist"))
    if len(info_plists) != 1:
        _fail(f"expected one application Info.plist in macOS package, found {len(info_plists)}")
    import plistlib

    with info_plists[0].open("rb") as stream:
        info = plistlib.load(stream)
    if info.get("DobbySourceCommit") != source_sha:
        _fail("macOS package source commit does not match the Release checkout")
    backend = next(expanded.rglob("dobbyvpn-backend"), None)
    if backend is None:
        _fail("macOS package does not contain its VPN backend")
    expected_arch = "arm64" if architecture == "arm64" else "x86_64"
    observed = _run("read macOS package backend architecture", ["lipo", "-archs", str(backend)], capture=True)
    architectures = (observed.stdout or b"").decode("utf-8", errors="replace").split()
    if architectures != [expected_arch]:
        _fail(f"macOS package backend architecture is {architectures!r}, expected {[expected_arch]!r}")


def _ensure_windows_pillow(work: Path, env: dict[str, str]) -> dict[str, str]:
    target = work / "python-packages"
    _run(
        "install pinned Windows icon packaging dependency",
        [
            sys.executable, "-m", "pip", "install", "--disable-pip-version-check",
            "--only-binary=:all:", "--target", str(target),
            "--requirement", str(NATIVE_UI_REQUIREMENTS),
        ],
        env=env,
    )
    updated = env.copy()
    existing = updated.get("PYTHONPATH")
    updated["PYTHONPATH"] = str(target) + (os.pathsep + existing if existing else "")
    return updated


def _build_windows_msi(
    archive: Path,
    service_directory: Path,
    version: str,
    source_sha: str,
    work: Path,
    environment: dict[str, str],
) -> Path:
    env = environment.copy()
    installer = work / "windows-installer"
    installer.mkdir()
    source_installer = ROOT / "installer" / "windows"
    for name in ("build.bat", "Package.wxs", "Folders.wxs", "AppComponents.wxs"):
        shutil.copyfile(source_installer / name, installer / name)
    shutil.copyfile(archive, installer / "dobbyVPN-windows.zip")
    for name in ("dobbyvpn-backend.exe", "dobby_bridge.dll", "wintun.dll"):
        shutil.copyfile(service_directory / name, installer / name)

    wix_tools = work / "wix-tools"
    wix_tools.mkdir()
    _run(
        "install WiX tool",
        ["dotnet", "tool", "install", "wix", "--version", "6.0.0", "--tool-path", str(wix_tools)],
        cwd=installer,
        env=env,
    )
    env["PATH"] = str(wix_tools) + os.pathsep + env.get("PATH", "")
    _run("Windows MSI build", ["cmd.exe", "/c", "build.bat"], cwd=installer, env=env)
    package = installer / "bin" / "amd64" / "dobbyVPN-windows-amd64.msi"
    log_path = installer / "bin" / "amd64" / "dobbyVPN-windows-amd64.msi.log"
    try:
        _verify_msi(package, version, env)
    finally:
        if log_path.exists():
            _emit_file("Windows MSI build log", log_path)
    return package


def _verify_msi(package: Path, version: str, env: dict[str, str]) -> None:
    script = r'''
$ErrorActionPreference = "Stop"
$msi = (Resolve-Path $env:DOBBYVPN_PACKAGE).Path
$installer = New-Object -ComObject WindowsInstaller.Installer
$database = $installer.GetType().InvokeMember("OpenDatabase", "InvokeMethod", $null, $installer, @($msi, 0))
$view = $database.GetType().InvokeMember("OpenView", "InvokeMethod", $null, $database, "SELECT Value FROM Property WHERE Property = 'ProductVersion'")
$view.GetType().InvokeMember("Execute", "InvokeMethod", $null, $view, $null)
$record = $view.GetType().InvokeMember("Fetch", "InvokeMethod", $null, $view, $null)
if ($null -eq $record) { throw "MSI ProductVersion property is missing" }
$actual = $record.GetType().InvokeMember("StringData", "GetProperty", $null, $record, 1)
Write-Output "MSI ProductVersion=$actual"
if ($actual -ne $env:DOBBYVPN_EXPECTED_VERSION) { throw "MSI version mismatch" }
$view = $database.GetType().InvokeMember("OpenView", "InvokeMethod", $null, $database, 'SELECT `FileName` FROM `File`')
$view.GetType().InvokeMember("Execute", "InvokeMethod", $null, $view, $null)
$files = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
while ($true) {
  $record = $view.GetType().InvokeMember("Fetch", "InvokeMethod", $null, $view, $null)
  if ($null -eq $record) { break }
  $name = $record.GetType().InvokeMember("StringData", "GetProperty", $null, $record, 1)
  [void]$files.Add(($name -split '\|')[-1])
}
foreach ($required in @("dobbyvpn-backend.exe", "dobby_bridge.dll", "wintun.dll")) {
  if (-not $files.Contains($required)) { throw "MSI is missing $required" }
}
'''
    verify_env = env.copy()
    verify_env["DOBBYVPN_PACKAGE"] = str(package)
    verify_env["DOBBYVPN_EXPECTED_VERSION"] = version
    _run(
        "Windows MSI content and version check",
        ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script],
        env=verify_env,
        timeout_seconds=WINDOWS_MSI_VERIFY_TIMEOUT_SECONDS,
    )


def _build_macos_pkg(
    archive: Path,
    service_directory: Path,
    architecture: str,
    version: str,
    source_sha: str,
    work: Path,
    environment: dict[str, str],
) -> Path:
    label = "aarch64" if architecture == "arm64" else "amd64"
    aliases = {"arm64": "arm64", "amd64": "amd64"}
    installer = work / "installer" / "macos"
    installer.mkdir(parents=True)
    # build.sh enters bin/<package-architecture> before it resolves the
    # service path '../../services/<architecture>/dobbyvpn-backend'. Stage
    # the binary under this installer directory so that relative path still
    # resolves after that cwd change.
    staging = installer / "services" / aliases[architecture]
    staging.mkdir(parents=True)
    shutil.copyfile(service_directory / "dobbyvpn-backend", staging / "dobbyvpn-backend")
    for name in ("build.sh", "postinstall.sh", "uninstall.sh", "vpnservice.plist"):
        shutil.copyfile(ROOT / "installer" / "macos" / name, installer / name)
    shutil.copyfile(archive, installer / f"dobbyVPN-macos-{label}.zip")
    env = environment.copy()
    env["APP_MAJOR_VERSION"], env["APP_MINOR_VERSION"], env["APP_MAINTENANCE_VERSION"] = version.split(".")
    env["GITHUB_SHA"] = source_sha
    _run("macOS installer package build", ["/bin/bash", "build.sh", architecture], cwd=installer, env=env)
    package = installer / "bin" / label / f"dobbyVPN-macos-{label}.pkg"
    _verify_pkg(package, version, work)
    return package


def _verify_pkg(package: Path, version: str, work: Path) -> None:
    expanded = work / "expanded-pkg"
    _run("expand macOS package for version check", ["pkgutil", "--expand-full", str(package), str(expanded)])
    records = list(expanded.rglob("PackageInfo"))
    if len(records) != 1:
        _fail(f"expected one PackageInfo in macOS package, found {len(records)}")
    import xml.etree.ElementTree as ET

    actual = ET.parse(records[0]).getroot().get("version")
    if actual != version:
        _fail(f"macOS package version {actual!r} does not match {version!r}")


def _verify_deb(package: Path, version: str, work: Path) -> None:
    result = _run(
        "Linux DEB version check",
        ["dpkg-deb", "-f", str(package), "Version"],
        capture=True,
    )
    actual = (result.stdout or b"").decode("utf-8", errors="replace").strip()
    if actual != version:
        _fail(f"DEB version {actual!r} does not match {version!r}")
    expanded = work / "expanded-deb"
    _run("expand Linux package for closure check", ["dpkg-deb", "-x", str(package), str(expanded)])
    service = expanded / "opt" / "dobbyvpn" / "bin" / "dobbyvpn-backend"
    cli = expanded / "opt" / "dobbyvpn" / "bin" / "dobby-cli"
    unit = expanded / "usr" / "lib" / "systemd" / "system" / "dobbyvpn.service"
    if not service.is_file() or not cli.is_file() or not unit.is_file():
        _fail("Linux DEB is missing its service, CLI, or systemd unit")
    library_path = expanded / "opt" / "dobbyvpn" / "lib"
    unit_text = unit.read_text(encoding="utf-8")
    if "ExecStart=/opt/dobbyvpn/bin/dobbyvpn-backend" not in unit_text:
        _fail("Linux DEB systemd unit does not start the packaged backend")
    control = work / "expanded-deb-control"
    _run("expand Linux package maintainer scripts", ["dpkg-deb", "-e", str(package), str(control)])
    expected_script_fragments = {
        "postinst": "systemctl enable dobbyvpn.service",
        "prerm": "systemctl disable dobbyvpn.service",
        "postrm": "systemctl daemon-reload",
    }
    for name, fragment in expected_script_fragments.items():
        script = control / name
        if not script.is_file() or fragment not in script.read_text(encoding="utf-8"):
            _fail(f"Linux DEB maintainer script {name} is missing {fragment!r}")
        _run(f"check Linux DEB {name} syntax", ["sh", "-n", str(script)])
    env = os.environ.copy()
    env["LD_LIBRARY_PATH"] = str(library_path)
    library_check = _run(
        "Linux DEB shared-library check",
        ["ldd", str(service)],
        env=env,
        capture=True,
    )
    if b"not found" in (library_check.stdout or b"") or b"not found" in (library_check.stderr or b""):
        _fail("Linux DEB service has an unresolved shared-library dependency")


def _install(args: argparse.Namespace) -> int:
    build = _read_json(args.build_descriptor, "desktop package descriptor")
    if build.get("schema") != 1 or build.get("mode") != "desktop-package":
        _fail("desktop package descriptor schema or mode is invalid")
    platform = build.get("platform")
    if platform not in PLATFORMS:
        _fail("desktop package descriptor platform is invalid")
    host, architecture = _select_host(str(platform), str(build.get("architecture", "")))
    package_path = Path(str(build.get("package_path", ""))).resolve(strict=True)
    expected_hash = build.get("package_sha256")
    if not isinstance(expected_hash, str) or _sha256(package_path) != expected_hash:
        _fail("desktop package hash does not match its descriptor")
    if build.get("architecture") != architecture:
        _fail("desktop package descriptor architecture does not match this host")
    run_dir = args.run_dir.resolve()
    run_dir.mkdir(parents=True, exist_ok=True)
    network = run_dir / ".dobbyvpn-run" / "s"
    network.parent.mkdir(parents=True, exist_ok=True)
    descriptor: dict[str, Any] = {
        "schema": 1,
        "mode": "installed-package",
        "platform": host,
        "architecture": architecture,
        "version": build["version"],
        "source_sha": build.get("source_sha"),
        "package_path": str(package_path),
        "package_sha256": expected_hash,
        "install_attempted": True,
        "installed": False,
        "network": str(network),
    }
    if host == "linux":
        descriptor.update(
            service="/opt/dobbyvpn/bin/dobbyvpn-backend",
            cli="/opt/dobbyvpn/bin/dobby-cli",
            library_path="/opt/dobbyvpn/lib",
        )
    elif host == "windows":
        program_files = os.environ.get("ProgramW6432") or os.environ.get("ProgramFiles")
        if not program_files:
            _fail("Windows Program Files directory is unavailable")
        binary_root = Path(program_files) / "DobbyVPN" / "bin"
        descriptor.update(
            service=str(binary_root / "dobbyvpn-backend.exe"),
            cli=str(binary_root / "dobby-cli.exe"),
            ui=str(binary_root / "DobbyVPN.exe"),
        )
    else:
        resources = Path("/Applications/Dobby VPN.app/Contents/Resources")
        descriptor.update(
            service=str(resources / "dobbyvpn-backend"),
            cli=str(resources / "dobby-cli"),
            ui="/Applications/Dobby VPN.app",
        )
    _write_json(args.output, descriptor)

    logs = run_dir / "logs"
    logs.mkdir(parents=True, exist_ok=True)
    msi_log = logs / "desktop-package-install.msi.log"
    if host == "linux":
        systemd_available = Path("/run/systemd/system").is_dir() and shutil.which("systemctl") is not None
        if systemd_available and any(
            path.is_file()
            for path in (
                Path("/usr/lib/systemd/system/dobbyvpn.service"),
                Path("/lib/systemd/system/dobbyvpn.service"),
            )
        ):
            _run("stop existing DobbyVPN service before package installation", ["sudo", "-n", "systemctl", "stop", "dobbyvpn.service"])
        _run("install Linux DEB", ["sudo", "-n", "dpkg", "-i", str(package_path)])
        if systemd_available:
            _run("keep packaged DobbyVPN service stopped for qualification", ["sudo", "-n", "systemctl", "stop", "dobbyvpn.service"])
    elif host == "windows":
        command = ["msiexec.exe", "/i", str(package_path), "/qn", "/norestart"]
        if args.control_pipe_sid:
            command.append(f"DOBBYVPN_CONTROL_PIPE_SID={args.control_pipe_sid}")
        command.extend(("/L*v", str(msi_log)))
        try:
            _run("install Windows MSI", command)
        finally:
            if msi_log.exists():
                _emit_file("Windows MSI install log", msi_log)
    else:
        uid = str(os.getuid())
        _run(
            "install macOS PKG",
            [
                "sudo", "-n", "env",
                f"DOBBYVPN_CONTROL_PEER_UID={uid}",
                f"DOBBY_LOG_PATH={run_dir / 'service.log'}",
                f"DOBBY_LOG_ROOT={run_dir}",
                "installer", "-pkg", str(package_path), "-target", "/",
            ],
        )

    required = [Path(descriptor["cli"]), Path(descriptor["service"])]
    if host == "windows":
        required.append(Path(descriptor["ui"]))
    if host == "macos":
        ui = Path(descriptor["ui"])
        if not ui.is_dir():
            _fail("installed macOS application bundle is missing")
    missing = [str(path) for path in required if not path.is_file()]
    if missing:
        _fail("installed package is missing expected files:\n" + "\n".join(missing))
    descriptor["installed"] = True
    _write_json(args.output, descriptor)
    _log(f"installed exact package {package_path} sha256={expected_hash}")
    return 0


def _uninstall(args: argparse.Namespace) -> int:
    descriptor = _read_json(args.installed_descriptor, "installed package descriptor")
    if descriptor.get("schema") != 1 or descriptor.get("mode") != "installed-package":
        _fail("installed package descriptor schema or mode is invalid")
    platform = descriptor.get("platform")
    if platform not in PLATFORMS:
        _fail("installed package descriptor platform is invalid")
    if descriptor.get("install_attempted") is not True:
        _log("package installation was not attempted; nothing to remove")
        return 0
    package = Path(str(descriptor.get("package_path", "")))
    run_dir = args.run_dir.resolve()
    logs = run_dir / "logs"
    logs.mkdir(parents=True, exist_ok=True)
    if platform == "linux":
        query = _run(
            "check Linux package installation state",
            ["dpkg-query", "-W", "-f=${db:Status-Status}", "dobby-vpn"],
            check=False,
            capture=True,
        )
        if query.returncode == 0:
            _run("remove Linux DEB", ["sudo", "-n", "dpkg", "-r", "dobby-vpn"])
        elif query.returncode != 1:
            _fail(f"dpkg-query exited {query.returncode} while checking cleanup state")
    elif platform == "windows":
        msi_log = logs / "desktop-package-uninstall.msi.log"
        try:
            result = _run(
                "remove Windows MSI",
                ["msiexec.exe", "/x", str(package), "/qn", "/norestart", "/L*v", str(msi_log)],
                check=False,
            )
            if result.returncode not in ({0} if descriptor.get("installed") else {0, 1605}):
                _fail(f"Windows MSI uninstall exited {result.returncode}")
        finally:
            if msi_log.exists():
                _emit_file("Windows MSI uninstall log", msi_log)
    else:
        uninstaller = Path("/usr/local/libexec/dobbyvpn-uninstall")
        if not uninstaller.is_file():
            uninstaller = ROOT / "installer" / "macos" / "uninstall.sh"
        _run("remove macOS PKG", ["sudo", "-n", str(uninstaller)])
    descriptor["installed"] = False
    descriptor["cleanup_complete"] = True
    _write_json(args.installed_descriptor, descriptor)
    return 0


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    commands.add_parser(
        "test",
        help="run the shared functional mini suite against an installed package",
        add_help=False,
    )
    build = commands.add_parser("build", help="build and package one native desktop target")
    build.add_argument("--platform", choices=PLATFORMS, required=True)
    build.add_argument("--arch", choices=("amd64", "arm64"))
    build.add_argument("--version")
    build.add_argument("--source-sha")
    build.add_argument("--output-dir", type=Path, required=True)
    build.add_argument("--skip-deps", action="store_true")
    describe = commands.add_parser("describe", help="validate and record a downloaded Release package")
    describe.add_argument("--platform", choices=PLATFORMS, required=True)
    describe.add_argument("--arch", choices=("amd64", "arm64"))
    describe.add_argument("--version", required=True)
    describe.add_argument("--source-sha", required=True)
    describe.add_argument("--package", type=Path, required=True)
    describe.add_argument("--output", type=Path, required=True)
    install = commands.add_parser("install", help="install the package described by a build result")
    install.add_argument("--build-descriptor", type=Path, required=True)
    install.add_argument("--run-dir", type=Path, required=True)
    install.add_argument("--output", type=Path, required=True)
    install.add_argument("--control-pipe-sid")
    uninstall = commands.add_parser("uninstall", help="remove the package installed by this run")
    uninstall.add_argument("--installed-descriptor", type=Path, required=True)
    uninstall.add_argument("--run-dir", type=Path, required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    arguments = list(sys.argv[1:] if argv is None else argv)
    if arguments and arguments[0] == "test":
        sys.path.insert(0, str(ROOT / "torturer"))
        from torturer_checks.desktop_platform import main as test_main

        return test_main(arguments)
    args = _parser().parse_args(arguments)
    if args.action == "build":
        return _build(args)
    if args.action == "describe":
        return _describe(args)
    if args.action == "install":
        return _install(args)
    if args.action == "uninstall":
        return _uninstall(args)
    _fail(f"unsupported command: {args.action}")
    return 2


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except DesktopPlatformError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(2)
