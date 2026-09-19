from __future__ import annotations

import importlib.util
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import unittest
import zipfile
from unittest import mock


SCRIPT = Path(__file__).with_name("package_desktop.py")
SPEC = importlib.util.spec_from_file_location("package_desktop", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
package_desktop = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(package_desktop)


class DesktopPackageTests(unittest.TestCase):
    def _payload(self, root: Path) -> None:
        services = root / "services"
        services.mkdir(parents=True)
        for name in (
            "dobby-vpn-ui",
            "dobby-cli",
            "ubuntu_grpcvpnserver",
            "libdobby_bridge.so",
            "libc++.so.1",
            "libc++abi.so.1",
        ):
            file = services / name
            file.write_bytes(b"payload-" + name.encode())
            file.chmod(0o755)
        for directory in ("macos-arm64", "macos-amd64"):
            target = services / directory
            target.mkdir()
            for name in ("dobby-vpn-ui", "dobby-cli"):
                file = target / name
                file.write_bytes(b"payload-" + directory.encode() + name.encode())
                file.chmod(0o755)
        windows_ui = services / "dobby-vpn-ui.exe"
        windows_cli = services / "dobby-cli.exe"
        windows_ui.write_bytes(b"MZ-ui")
        windows_cli.write_bytes(b"MZ-cli")
        windows_ui.chmod(0o755)
        windows_cli.chmod(0o755)

    def test_native_archives_use_installer_compatible_layouts(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self._payload(root)
            (root / "services" / "dobby-vpn-ui.exe").chmod(0o644)
            (root / "services" / "dobby-cli.exe").chmod(0o644)
            output = root / "output"
            with (
                mock.patch.object(package_desktop, "SERVICES", root / "services"),
                mock.patch.object(package_desktop, "LOGO", root / "logo.png"),
                mock.patch.object(
                    package_desktop,
                    "create_windows_icon",
                    side_effect=lambda destination: (
                        destination.parent.mkdir(parents=True, exist_ok=True),
                        destination.write_bytes(b"ico"),
                    )[-1],
                ),
            ):
                package_desktop.package_windows("1.5.1", output)
                package_desktop.package_macos(
                    "1.5.1",
                    output,
                    arch="aarch64",
                    source_dir=root / "services",
                    minimum_system_version="12.0",
                )

            with zipfile.ZipFile(output / "dobby-vpn-1.5.1-windows-amd64.zip") as archive:
                names = set(archive.namelist())
            self.assertIn("bin/Dobby Vpn.exe", names)
            self.assertIn("bin/dobby-cli.exe", names)
            self.assertIn("app/app.ico", names)

            with zipfile.ZipFile(output / "dobby-vpn-1.5.1-mac-aarch64.zip") as archive:
                names = set(archive.namelist())
                info = plistlib.loads(
                    archive.read("Dobby Vpn.app/Contents/Info.plist")
                )
            self.assertIn("Dobby Vpn.app/Contents/MacOS/Dobby Vpn", names)
            self.assertIn("Dobby Vpn.app/Contents/Resources/dobby-cli", names)
            self.assertIn("Dobby Vpn.app/Contents/Info.plist", names)
            self.assertEqual(info["LSMinimumSystemVersion"], "12.0")

    def test_version_is_strict_numeric_semver(self) -> None:
        self.assertTrue(package_desktop.VERSION_RE.fullmatch("1.5.1"))
        self.assertFalse(package_desktop.VERSION_RE.fullmatch("1.5"))
        self.assertFalse(package_desktop.VERSION_RE.fullmatch("v1.5.1"))

    def test_staging_root_keeps_architecture_payloads_separate(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            staging = root / "staging"
            for name in ("windows-amd64", "macos-arm64", "macos-amd64", "linux-amd64"):
                (staging / name).mkdir(parents=True)
            output = root / "output"
            calls: list[tuple[str, Path]] = []

            def capture_windows(version: str, destination: Path, *, source_dir: Path) -> None:
                calls.append(("windows", source_dir))

            def capture_macos(version: str, destination: Path, *, arch: str, source_dir: Path, minimum_system_version: str) -> None:
                calls.append((arch, source_dir))

            def capture_linux(version: str, destination: Path, *, source_dir: Path) -> None:
                calls.append(("linux", source_dir))

            with (
                mock.patch.object(package_desktop, "package_windows", side_effect=capture_windows),
                mock.patch.object(package_desktop, "package_macos", side_effect=capture_macos),
                mock.patch.object(package_desktop, "package_linux", side_effect=capture_linux),
                mock.patch.object(
                    sys,
                    "argv",
                    [
                        "package_desktop.py",
                        "--version",
                        "1.5.1",
                        "--staging-root",
                        str(staging),
                        "--output",
                        str(output),
                    ],
                ),
            ):
                package_desktop.main()

            self.assertEqual(
                calls,
                [
                    ("windows", staging / "windows-amd64"),
                    ("aarch64", staging / "macos-arm64"),
                    ("amd64", staging / "macos-amd64"),
                    ("linux", staging / "linux-amd64"),
                ],
            )

    def test_linux_package_keeps_runtime_path_aligned_with_payload(self) -> None:
        if package_desktop.shutil.which("dpkg-deb") is None:
            self.skipTest("dpkg-deb is unavailable")
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self._payload(root)
            output = root / "output"
            with mock.patch.object(package_desktop, "SERVICES", root / "services"):
                package_desktop.package_linux("1.5.1", output)

            package = output / "dobby-vpn_1.5.1_amd64.deb"
            extracted = root / "extracted"
            subprocess.run(["dpkg-deb", "-x", str(package), str(extracted)], check=True)
            unit = extracted / "usr/local/lib/systemd/system/dobbyvpn.service"
            self.assertIn(
                "Environment=LD_LIBRARY_PATH=/opt/dobbyvpn/lib/runtime",
                unit.read_text(encoding="utf-8"),
            )
            self.assertTrue(
                (extracted / "opt/dobbyvpn/lib/runtime/libc++.so.1").is_file()
            )


if __name__ == "__main__":
    unittest.main()
