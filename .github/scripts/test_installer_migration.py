from __future__ import annotations

import hashlib
import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("installer_migration.py")
SPEC = importlib.util.spec_from_file_location("installer_migration", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT_PATH}")
installer_migration = importlib.util.module_from_spec(SPEC)
import sys

sys.modules[SPEC.name] = installer_migration
SPEC.loader.exec_module(installer_migration)


class _Response(io.BytesIO):
    def __enter__(self) -> "_Response":
        return self

    def __exit__(self, *_: object) -> None:
        self.close()


class _FakeAdapter(installer_migration.InstallerAdapter):
    def __init__(self) -> None:
        self.calls: list[tuple[str, str, bool | None]] = []

    def install(self, package: Path, *, label: str) -> None:
        self.calls.append(("install", label, None))

    def uninstall(self, package: Path, *, label: str, allow_missing: bool = False) -> None:
        self.calls.append(("uninstall", label, allow_missing))

    def verify_installed(self, expected_version: str, *, label: str) -> None:
        self.calls.append(("verify_installed", label, None))

    def verify_uninstalled(self, *, label: str) -> None:
        self.calls.append(("verify_uninstalled", label, None))


class InstallerMigrationTests(unittest.TestCase):
    def test_manifest_pins_published_v150_desktop_assets(self) -> None:
        assets = installer_migration.load_manifest()
        self.assertEqual(assets["windows"].sha256, "187e3c14c44746b8fbff328cfc24605359eb22ac2f224159703d9b0ea5316c79")
        self.assertEqual(assets["macos-aarch64"].sha256, "17377193dcd93885a3ff475690db2a9542a63e6b2926d051ee2ccd9da1f10422")
        self.assertEqual(assets["macos-amd64"].sha256, "9788350f4b1b0b62845d9d4add7b59c671f3ea658e41ceaa82e55404649334fa")
        self.assertEqual(assets["windows"].size, 142204928)

    def test_manifest_rejects_mutable_or_unrelated_asset_url(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "manifest.json"
            path.write_text(
                (installer_migration.DEFAULT_MANIFEST.read_text(encoding="utf-8")
                 .replace("https://github.com/DobbyVPN/DobbyVPN/releases/download/v1.5.0/", "https://example.invalid/")),
                encoding="utf-8",
            )
            with self.assertRaises(installer_migration.MigrationError):
                installer_migration.load_manifest(path)

    def test_download_verifies_size_and_digest_before_rename(self) -> None:
        data = b"pinned package fixture"
        asset = installer_migration.RollbackAsset(
            "windows", "fixture.msi", "https://example.invalid/fixture.msi",
            hashlib.sha256(data).hexdigest(), len(data),
        )
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / asset.name
            result = installer_migration.download_asset(
                asset, destination, opener=lambda *_args, **_kwargs: _Response(data),
            )
            self.assertEqual(result, destination)
            self.assertEqual(destination.read_bytes(), data)
            self.assertFalse(destination.with_name(f".{destination.name}.partial").exists())

    def test_download_rejects_digest_mismatch_and_removes_partial(self) -> None:
        asset = installer_migration.RollbackAsset(
            "windows", "fixture.msi", "https://example.invalid/fixture.msi", "0" * 64, 4,
        )
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / asset.name
            with self.assertRaises(installer_migration.MigrationError):
                installer_migration.download_asset(
                    asset, destination, opener=lambda *_args, **_kwargs: _Response(b"bad!"),
                )
            self.assertFalse(destination.exists())
            self.assertFalse(destination.with_name(f".{destination.name}.partial").exists())

    def test_transition_sequence_is_explicit_and_ends_empty(self) -> None:
        adapter = _FakeAdapter()
        installer_migration.migration_sequence(adapter, Path("current"), Path("previous"))
        self.assertEqual(
            adapter.calls,
            [
                ("uninstall", "baseline-uninstall", True),
                ("verify_uninstalled", "baseline-empty", None),
                ("install", "fresh-install", None),
                ("verify_installed", "fresh-installed", None),
                ("uninstall", "fresh-uninstall", False),
                ("verify_uninstalled", "fresh-uninstalled", None),
                ("install", "old-install", None),
                ("verify_installed", "old-installed", None),
                ("install", "upgrade-install", None),
                ("verify_installed", "upgraded-to-current", None),
                ("uninstall", "rollback-uninstall-current", False),
                ("verify_uninstalled", "rollback-empty", None),
                ("install", "rollback-install-old", None),
                ("verify_installed", "rolled-back-to-old", None),
                ("uninstall", "final-uninstall", False),
                ("verify_uninstalled", "final-empty", None),
            ],
        )

    def test_qualify_removes_downloaded_previous_package_on_failure(self) -> None:
        fake_adapter = _FakeAdapter()
        downloaded: list[Path] = []

        def fake_download(asset: object, destination: Path, **_: object) -> Path:
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(b"rollback")
            downloaded.append(destination)
            return destination

        with tempfile.TemporaryDirectory() as temporary:
            current = Path(temporary) / "current.msi"
            current.write_bytes(b"current")
            with (
                mock.patch.object(installer_migration, "download_asset", side_effect=fake_download),
                mock.patch.object(installer_migration, "WindowsInstaller", return_value=fake_adapter),
                mock.patch.object(
                    installer_migration,
                    "migration_sequence",
                    side_effect=installer_migration.MigrationError("fixture failure"),
                ),
            ):
                with self.assertRaises(installer_migration.MigrationError):
                    installer_migration.qualify("windows", current)
        self.assertEqual(len(downloaded), 1)
        self.assertFalse(downloaded[0].exists())
        self.assertFalse(downloaded[0].parent.exists())
        self.assertEqual(
            fake_adapter.calls,
            [
                ("uninstall", "failure-uninstall-current", True),
                ("uninstall", "failure-uninstall-previous", True),
                ("verify_uninstalled", "failure-empty", None),
            ],
        )

    def test_macos_uninstaller_is_the_fixed_product_owned_path(self) -> None:
        script = (SCRIPT_PATH.parents[2] / "installer/macos/uninstall.sh").read_text(encoding="utf-8")
        self.assertIn('PLIST_DEST="/Library/LaunchDaemons/com.dobby.vpnservice.plist"', script)
        self.assertIn('APP_BUNDLE="/Applications/Dobby VPN.app"', script)
        self.assertIn('CONTROL_SOCKET="/var/run/dobbyvpn/control.sock"', script)
        self.assertIn('launchctl bootout system/com.dobby.vpnservice', script)
        self.assertIn('pkgutil --forget com.dobby.pkg', script)
        self.assertNotIn("$1", script)

        build = (SCRIPT_PATH.parents[2] / "installer/macos/build.sh").read_text(encoding="utf-8")
        self.assertIn("cp ../../uninstall.sh Payload/usr/local/libexec/dobbyvpn-uninstall", build)
        self.assertIn("--install-location / \\", build)

    def test_macos_uninstall_falls_back_to_checked_out_product_script(self) -> None:
        class Runner:
            def __init__(self) -> None:
                self.commands: list[list[str]] = []

            def run(self, command: list[str], **_: object) -> None:
                self.commands.append(command)

        with tempfile.TemporaryDirectory() as temporary:
            fallback = Path(temporary) / "uninstall.sh"
            fallback.write_text("#!/bin/sh\n", encoding="utf-8")
            runner = Runner()
            adapter = installer_migration.MacOSInstaller(
                runner, fallback_uninstaller=fallback  # type: ignore[arg-type]
            )
            adapter.uninstall(Path("old.pkg"), label="old-uninstall")
            self.assertEqual(runner.commands, [["sudo", "-n", str(fallback)]])

        verify = (SCRIPT_PATH.read_text(encoding="utf-8"))
        self.assertIn('if [ "$EXPECTED_VERSION" = "1.5.1" ]; then', verify)

    def test_windows_verification_covers_service_lifecycle(self) -> None:
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        self.assertIn("Get-Service -Name 'DobbyVPN Server' -ErrorAction Stop", source)
        self.assertIn("DobbyVPN Server is not running", source)
        self.assertIn("DobbyVPN Server remains registered", source)

    def test_windows_arp_results_remain_arrays_for_zero_or_one_entry(self) -> None:
        source = SCRIPT_PATH.read_text(encoding="utf-8")
        query = """$entries = @(
  @(
    Get-ItemProperty 'HKLM:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\*' -ErrorAction SilentlyContinue
    Get-ItemProperty 'HKLM:\\Software\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\*' -ErrorAction SilentlyContinue
  ) | Where-Object { $_.DisplayName -eq 'DobbyVPN' }
)"""
        self.assertEqual(source.count(query), 2)
        self.assertIn("if ($entries.Count -ne 1)", source)
        self.assertIn("if ($entries.Count -ne 0)", source)

    def test_workflow_qualifies_both_macos_architectures(self) -> None:
        workflow = (SCRIPT_PATH.parents[1] / "workflows/installer_migration.yml").read_text(
            encoding="utf-8"
        )
        self.assertIn("runner: macos-15\n", workflow)
        self.assertIn("runner: macos-15-intel\n", workflow)
        self.assertIn("artifact: dobbyVPN-macos-aarch64.pkg", workflow)
        self.assertIn("artifact: dobbyVPN-macos-amd64.pkg", workflow)


if __name__ == "__main__":
    unittest.main()
