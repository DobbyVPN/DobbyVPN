#!/usr/bin/env python3
"""Assemble the native desktop archives consumed by the platform installers.

The Go/Fyne binaries are built on their native runners.  This script only
assembles their already-built payloads and therefore runs on the Linux desktop
packaging job without Java, Gradle, or a second application runtime.
"""

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


def copy_executable(source: Path, destination: Path, *, executable: bool = True) -> None:
    required(source, executable=executable)
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(destination.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


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


def create_windows_icon(destination: Path) -> None:
    required(LOGO)
    try:
        from PIL import Image
    except ImportError as error:
        fail(f"Pillow is required to create the Windows icon: {error}")
    try:
        image = Image.open(LOGO).convert("RGBA")
        destination.parent.mkdir(parents=True, exist_ok=True)
        image.save(destination, format="ICO", sizes=[(256, 256), (128, 128), (64, 64), (32, 32), (16, 16)])
    except Exception as error:  # Pillow errors are more useful with the source path.
        fail(f"could not convert {LOGO} to Windows icon: {error}")


def package_windows(version: str, output: Path, *, source_dir: Path | None = None) -> None:
    source_dir = SERVICES if source_dir is None else source_dir
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-desktop-windows-") as temporary:
        root = Path(temporary)
        copy_executable(
            source_dir / "dobby-vpn-ui.exe",
            root / "bin" / "Dobby Vpn.exe",
            executable=False,
        )
        copy_executable(
            source_dir / "dobby-cli.exe",
            root / "bin" / "dobby-cli.exe",
            executable=False,
        )
        create_windows_icon(root / "app" / "app.ico")
        write_zip(root, output / f"dobby-vpn-{version}-windows-amd64.zip")


def mac_info(version: str, minimum_system_version: str) -> dict[str, object]:
    return {
        "CFBundleDisplayName": "Dobby Vpn",
        "CFBundleExecutable": "Dobby Vpn",
        "CFBundleIdentifier": "com.dobby.vpn",
        "CFBundleName": "Dobby Vpn",
        "CFBundlePackageType": "APPL",
        "CFBundleShortVersionString": version,
        "CFBundleVersion": version,
        "LSMinimumSystemVersion": minimum_system_version,
    }


def package_macos(version: str, output: Path, *, arch: str, source_dir: Path, minimum_system_version: str) -> None:
    with tempfile.TemporaryDirectory(prefix=f"dobbyvpn-desktop-macos-{arch}-") as temporary:
        bundle = Path(temporary) / "Dobby Vpn.app"
        executable = bundle / "Contents" / "MacOS" / "Dobby Vpn"
        resources = bundle / "Contents" / "Resources"
        copy_executable(source_dir / "dobby-vpn-ui", executable)
        copy_executable(source_dir / "dobby-cli", resources / "dobby-cli")
        info = bundle / "Contents" / "Info.plist"
        info.parent.mkdir(parents=True, exist_ok=True)
        with info.open("wb") as handle:
            plistlib.dump(mac_info(version, minimum_system_version), handle, sort_keys=True)
        write_zip(Path(temporary), output / f"dobby-vpn-{version}-mac-{arch}.zip")


def write_linux_control(debian: Path, version: str) -> None:
    debian.mkdir(parents=True, exist_ok=True)
    (debian / "control").write_text(
        """Package: dobby-vpn
Version: {version}
Section: net
Priority: optional
Architecture: amd64
Maintainer: DobbyVPN team
Description: DobbyVPN native desktop client
 The Go/Fyne desktop client and its VPN service runtime.
""".format(version=version),
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


def package_linux(version: str, output: Path, *, source_dir: Path | None = None) -> None:
    source_dir = SERVICES if source_dir is None else source_dir
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-desktop-linux-") as temporary:
        root = Path(temporary)
        copy_executable(source_dir / "dobby-vpn-ui", root / "opt" / "dobbyvpn" / "bin" / "dobby-vpn")
        copy_executable(source_dir / "dobby-cli", root / "opt" / "dobbyvpn" / "bin" / "dobby-cli")
        copy_executable(source_dir / "ubuntu_grpcvpnserver", root / "opt" / "dobbyvpn" / "lib" / "app" / "ubuntu_grpcvpnserver")
        for name in ("libdobby_bridge.so", "libc++.so.1", "libc++abi.so.1"):
            source = source_dir / name
            required(source)
            destination = root / "opt" / "dobbyvpn" / "lib" / "runtime" / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, destination)
            destination.chmod(0o755)

        unit = root / "usr" / "local" / "lib" / "systemd" / "system" / "dobbyvpn.service"
        unit.parent.mkdir(parents=True, exist_ok=True)
        unit.write_text(
            """[Unit]
Description=DobbyVPN gRPC service
After=network-online.target

[Service]
Type=simple
Environment=LD_LIBRARY_PATH=/opt/dobbyvpn/lib/runtime
ExecStart=/opt/dobbyvpn/lib/app/ubuntu_grpcvpnserver
Restart=always
RestartSec=2

[Install]
Alias=dobbyvpn.service
WantedBy=multi-user.target
""",
            encoding="utf-8",
        )

        desktop = root / "usr" / "share" / "applications" / "dobby-vpn.desktop"
        desktop.parent.mkdir(parents=True, exist_ok=True)
        desktop.write_text(
            """[Desktop Entry]
Type=Application
Name=Dobby Vpn
Exec=/opt/dobbyvpn/bin/dobby-vpn
Terminal=false
Categories=Network;
""",
            encoding="utf-8",
        )
        link = root / "usr" / "local" / "bin" / "dobby-vpn"
        link.parent.mkdir(parents=True, exist_ok=True)
        link.symlink_to("/opt/dobbyvpn/bin/dobby-vpn")
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
            fail("dpkg-deb is required to build the Linux desktop package")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Package the native DobbyVPN desktop binaries.")
    parser.add_argument("--version", required=True, help="Marketing version x.y.z")
    parser.add_argument("--output", type=Path, default=ROOT / "output")
    parser.add_argument(
        "--staging-root",
        type=Path,
        default=SERVICES,
        help="Directory containing the native desktop payloads",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    if not VERSION_RE.fullmatch(args.version):
        fail("--version must be a numeric x.y.z version")
    output = args.output.resolve()
    staging = args.staging_root.resolve()
    package_windows(args.version, output, source_dir=staging / "windows-amd64" if (staging / "windows-amd64").is_dir() else staging)
    package_macos(
        args.version,
        output,
        arch="aarch64",
        source_dir=staging / "macos-arm64" if (staging / "macos-arm64").is_dir() else staging,
        minimum_system_version="12.0",
    )
    package_macos(
        args.version,
        output,
        arch="amd64",
        source_dir=staging / "macos-amd64",
        minimum_system_version="12.0",
    )
    # Keep the Linux payload at the historical staging root.  It is also the
    # only payload whose names overlap with the arm64 macOS CLI/UI.
    package_linux(args.version, output, source_dir=staging / "linux-amd64" if (staging / "linux-amd64").is_dir() else staging)
    print(f"[+] Wrote native desktop packages to {output}")


if __name__ == "__main__":
    main()
