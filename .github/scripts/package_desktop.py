#!/usr/bin/env python3
"""Assemble desktop packages from native Go backend and UI build artifacts."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import plistlib
import re
import shutil
import stat
import subprocess
import tempfile
import zipfile


ROOT = Path(__file__).resolve().parents[2]
SERVICES = ROOT / "runtime" / "services"
LOGO = ROOT / "assets" / "logo.png"
VERSION_RE = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
ZIP_DATE = (1980, 1, 1, 0, 0, 0)


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"[!] {message}")


def required(path: Path, *, executable: bool = False) -> Path:
    if not path.is_file():
        fail(f"required desktop payload is missing: {path}")
    if executable and not os.access(path, os.X_OK):
        fail(f"required desktop payload is not executable: {path}")
    return path


def copy_file(source: Path, destination: Path, *, executable: bool = False) -> None:
    required(source, executable=executable)
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    mode = destination.stat().st_mode
    if executable:
        mode |= stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH
    destination.chmod(mode)


def copy_directory(source: Path, destination: Path) -> None:
    if not source.is_dir():
        fail(f"required native UI output directory is missing: {source}")
    for path in sorted(source.rglob("*")):
        if path.is_symlink():
            fail(f"native UI output contains an unexpected symbolic link: {path}")
        if path.is_file():
            target = destination / path.relative_to(source)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)
            target.chmod(path.stat().st_mode)


def write_zip(root: Path, output: Path) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for path in sorted(root.rglob("*")):
            if not path.is_file():
                continue
            relative = path.relative_to(root).as_posix()
            info = zipfile.ZipInfo(relative, ZIP_DATE)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (path.stat().st_mode & 0xFFFF) << 16
            archive.writestr(info, path.read_bytes())


def windows_icon(destination: Path) -> None:
    required(LOGO)
    try:
        from PIL import Image
    except ImportError as error:
        fail(f"Pillow is required to create the Windows icon: {error}")
    try:
        image = Image.open(LOGO).convert("RGBA")
        destination.parent.mkdir(parents=True, exist_ok=True)
        image.save(destination, format="ICO", sizes=[(256, 256), (128, 128), (64, 64), (32, 32), (16, 16)])
    except Exception as error:
        fail(f"could not convert {LOGO} to a Windows icon: {error}")


def package_windows(version: str, output: Path, source: Path) -> None:
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-desktop-windows-") as temporary:
        root = Path(temporary)
        binary = root / "bin"
        copy_directory(source / "frontend", binary)
        for name in ("DobbyVPN.exe", "DobbyVPN.dll", "dobby-cli.exe", "dobbyvpn-backend.exe", "dobby_bridge.dll", "wintun.dll"):
            staged = source / name
            if staged.exists():
                copy_file(staged, binary / name, executable=False)
        for name in ("dobby-cli.exe", "dobbyvpn-backend.exe", "dobby_bridge.dll", "wintun.dll"):
            required(binary / name)
        required(binary / "DobbyVPN.exe")
        windows_icon(root / "app" / "app.ico")
        write_zip(root, output / f"dobby-vpn-{version}-windows-amd64.zip")


def package_macos(version: str, output: Path, *, arch: str, source: Path) -> None:
    with tempfile.TemporaryDirectory(prefix=f"dobbyvpn-desktop-macos-{arch}-") as temporary:
        root = Path(temporary)
        bundle = root / "Dobby VPN.app"
        contents = bundle / "Contents"
        executable = contents / "MacOS" / "DobbyVPNMacApp"
        resources = contents / "Resources"
        copy_file(source / "DobbyVPNMacApp", executable, executable=True)
        copy_file(source / "dobbyvpn-backend", resources / "dobbyvpn-backend", executable=True)
        copy_file(source / "dobby-cli", resources / "dobby-cli", executable=True)
        if arch == "amd64":
            copy_file(source / "trusttunnel_client", resources / "trusttunnel_client", executable=True)
        info = {
            "CFBundleDisplayName": "Dobby VPN",
            "CFBundleExecutable": "DobbyVPNMacApp",
            "CFBundleIdentifier": "vpn.dobby.desktop",
            "CFBundleName": "Dobby VPN",
            "CFBundlePackageType": "APPL",
            "CFBundleShortVersionString": version,
            "CFBundleVersion": version,
            "DobbySourceCommit": os.environ.get("GITHUB_SHA", "N/A"),
            "LSMinimumSystemVersion": "12.0",
        }
        with (contents / "Info.plist").open("wb") as handle:
            plistlib.dump(info, handle, sort_keys=True)
        write_zip(root, output / f"dobby-vpn-{version}-mac-{arch}.zip")


def write_linux_control(debian: Path, version: str) -> None:
    debian.mkdir(parents=True, exist_ok=True)
    (debian / "control").write_text(
        f"""Package: dobby-vpn
Version: {version}
Section: net
Priority: optional
Architecture: amd64
Maintainer: DobbyVPN team
Description: DobbyVPN Go backend and command line client
 Shared Go VPN backend and operator CLI for Linux.
""",
        encoding="utf-8",
    )
    postinst = debian / "postinst"
    postinst.write_text(
        """#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    systemctl enable dobbyvpn.service || true
fi
exit 0
""",
        encoding="utf-8",
    )
    postinst.chmod(0o755)


def package_linux(version: str, output: Path, source: Path) -> None:
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-desktop-linux-") as temporary:
        root = Path(temporary)
        app = root / "opt" / "dobbyvpn"
        copy_file(source / "dobbyvpn-backend", app / "bin" / "dobbyvpn-backend", executable=True)
        copy_file(source / "dobby-cli", app / "bin" / "dobby-cli", executable=True)
        for name in ("libdobby_bridge.so", "libc++.so.1", "libc++abi.so.1"):
            copy_file(source / name, app / "lib" / name)

        unit = root / "usr" / "lib" / "systemd" / "system" / "dobbyvpn.service"
        unit.parent.mkdir(parents=True, exist_ok=True)
        unit.write_text(
            """[Unit]
Description=DobbyVPN Go backend
After=network-online.target

[Service]
Type=simple
Environment=LD_LIBRARY_PATH=/opt/dobbyvpn/lib
ExecStart=/opt/dobbyvpn/bin/dobbyvpn-backend
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
""",
            encoding="utf-8",
        )
        link = root / "usr" / "local" / "bin" / "dobby-cli"
        link.parent.mkdir(parents=True, exist_ok=True)
        link.symlink_to("/opt/dobbyvpn/bin/dobby-cli")
        write_linux_control(root / "DEBIAN", version)
        output_path = output / f"dobby-vpn_{version}_amd64.deb"
        output_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            subprocess.run(
                ["dpkg-deb", "--build", "--root-owner-group", str(root), str(output_path)],
                check=True,
                text=True,
                env={**os.environ, "SOURCE_DATE_EPOCH": "0"},
            )
        except FileNotFoundError:
            fail("dpkg-deb is required to build the Linux package")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Package native DobbyVPN desktop payloads.")
    parser.add_argument("--version", required=True, help="Marketing version x.y.z")
    parser.add_argument("--output", type=Path, default=ROOT / "output")
    parser.add_argument("--staging-root", type=Path, default=SERVICES)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    if not VERSION_RE.fullmatch(args.version):
        fail("--version must be a numeric x.y.z version")
    output = args.output.resolve()
    staging = args.staging_root.resolve()
    package_windows(args.version, output, staging / "windows-amd64")
    package_macos(args.version, output, arch="aarch64", source=staging / "macos-arm64")
    package_macos(args.version, output, arch="amd64", source=staging / "macos-amd64")
    package_linux(args.version, output, staging / "linux-amd64")
    print(f"[+] Wrote desktop packages to {output}")


if __name__ == "__main__":
    main()
