from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest


PRODUCT_ROOT = Path(__file__).resolve().parents[4]
WINDOWS_COMPONENTS = PRODUCT_ROOT / "ui/windows/installer/AppComponents.wxs"
MIGRATION_PATH = PRODUCT_ROOT / ".github/scripts/desktop/installer_migration.py"
TORTURER_ROOT = PRODUCT_ROOT / "torturer"
if str(TORTURER_ROOT) not in sys.path:
    sys.path.insert(0, str(TORTURER_ROOT))
from torturer_runner import local_vm  # noqa: E402

SPEC = importlib.util.spec_from_file_location("dobbyvpn_installer_migration_test", MIGRATION_PATH)
if SPEC is None or SPEC.loader is None:
    raise AssertionError(f"could not load {MIGRATION_PATH}")
installer_migration = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = installer_migration
SPEC.loader.exec_module(installer_migration)


class RecordingRunner:
    def __init__(self) -> None:
        self.calls: list[tuple[list[str], dict[str, object]]] = []

    def run(self, command: list[str], **kwargs: object) -> None:
        self.calls.append((command, kwargs))


class WindowsProtocolSchemeTests(unittest.TestCase):
    def test_native_case_command_selects_case_without_legacy_smoke_module(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            run_dir = root / "run"
            source_root = run_dir / "source"
            ui = source_root / "torturer" / "torturer_runner" / "ui"
            ui.mkdir(parents=True)
            (source_root / "VERSION").write_text("1.5.4\n", encoding="utf-8")
            (ui / "journey.py").touch()
            helper = root / "NativeUI.exe"
            helper.touch()

            command = local_vm._native_ui_command(
                run_dir,
                {"cli": "cli.exe", "ui": "ui.exe"},
                {"pid": 42, "binary": "service.exe", "pipe": "DobbyVPN.Control"},
                "windows",
                30,
                helper,
                native_cases=("configure-acceptance",),
                source_sha="a" * 40,
            )

        self.assertEqual(
            [command[index + 1] for index, value in enumerate(command[:-1])
             if value == "--native-case"],
            ["configure-acceptance"],
        )
        self.assertEqual(command[command.index("--candidate-version") + 1], "1.5.4")
        self.assertEqual(command[command.index("--source-sha") + 1], "a" * 40)

    def test_native_application_sha_uses_reused_package_identity(self) -> None:
        app_sha = "a" * 40
        test_sha = "b" * 40
        self.assertEqual(local_vm._native_application_sha({"source_sha": app_sha}, test_sha), app_sha)
        self.assertEqual(local_vm._native_application_sha({}, test_sha), test_sha)
        with self.assertRaisesRegex(local_vm.LocalVMError, "application source SHA"):
            local_vm._native_application_sha({"source_sha": "invalid"}, test_sha)

    def test_msi_lifecycle_probes_registration_command_and_removal(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            package = Path(directory) / "dobbyVPN-windows-amd64.msi"
            package.touch()
            runner = RecordingRunner()
            installer = installer_migration.WindowsInstaller(
                runner,
                current_package=package,
                control_pipe_sid=None,
            )

            installer.verify_installed("1.5.4", label="current-installed")
            installer.verify_uninstalled(label="current-uninstalled")

        self.assertEqual(len(runner.calls), 2)
        installed_script = runner.calls[0][0][-1]
        self.assertIn("[version]$env:DOBBYVPN_EXPECTED_VERSION -ge [version]'1.5.4'", installed_script)
        self.assertIn("HKLM:\\Software\\Classes\\dobbyvpn", installed_script)
        self.assertIn("$scheme.GetValue('URL Protocol', $null)", installed_script)
        self.assertIn(
            "$scheme.GetValue('URL Protocol', $null)) { throw \"URL Protocol marker is missing\" }",
            installed_script,
        )
        self.assertIn("$expected = '\"' + (Join-Path $root 'bin\\DobbyVPN.exe') + '\" \"%1\"'", installed_script)
        self.assertIn('if ($command -ne $expected) { throw "unexpected protocol command: $command" }', installed_script)
        self.assertEqual(runner.calls[0][1]["environment"]["DOBBYVPN_EXPECTED_VERSION"], "1.5.4")

        components = WINDOWS_COMPONENTS.read_text(encoding="utf-8")
        for assertion in (
            'Id="DobbyVPNProtocol"',
            'Key="Software\\Classes\\dobbyvpn" ForceDeleteOnUninstall="yes"',
            'Name="URL Protocol" Type="string" Value="" KeyPath="yes"',
            'Key="shell\\open\\command"',
            'Value="&quot;[DobbyVPNFolderBin]DobbyVPN.exe&quot; &quot;%1&quot;"',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, components)

        uninstalled_script = runner.calls[1][0][-1]
        self.assertIn("Test-Path 'HKLM:\\Software\\Classes\\dobbyvpn'", uninstalled_script)
        self.assertIn('DobbyVPN URL scheme remains registered after uninstall', uninstalled_script)


if __name__ == "__main__":
    unittest.main()
