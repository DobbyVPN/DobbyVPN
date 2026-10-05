from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest


PRODUCT_ROOT = Path(__file__).resolve().parents[4]
WINDOWS_PROGRAM = PRODUCT_ROOT / "ui/windows/DobbyVPN.Windows/Program.cs"
WINDOWS_NATIVE_UI = PRODUCT_ROOT / "torturer/native_ui/windows/Program.cs"
MIGRATION_PATH = PRODUCT_ROOT / ".github/scripts/desktop/installer_migration.py"
SPEC = importlib.util.spec_from_file_location("dobbyvpn_installer_migration_test", MIGRATION_PATH)
if SPEC is None or SPEC.loader is None:
    raise AssertionError(f"could not load {MIGRATION_PATH}")
installer_migration = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = installer_migration
SPEC.loader.exec_module(installer_migration)


class _RecordingRunner:
    def __init__(self, log_dir: Path) -> None:
        self.log_dir = log_dir
        self.calls: list[tuple[list[str], dict[str, object]]] = []

    def run(self, command: list[str], **kwargs: object) -> None:
        self.calls.append((command, kwargs))


class WindowsProtocolSchemeTests(unittest.TestCase):
    def test_secondary_instance_awaits_activation_redirection_without_blocking_sta(self) -> None:
        source = WINDOWS_PROGRAM.read_text(encoding="utf-8")

        self.assertIn("public static async Task Main(string[] args)", source)
        self.assertIn("await instance.RedirectActivationToAsync(activation);", source)
        self.assertNotIn("GetAwaiter().GetResult()", source)

    def test_native_ui_helper_checks_narrow_render_and_clipboard_availability(self) -> None:
        source = WINDOWS_NATIVE_UI.read_text(encoding="utf-8")

        for assertion in (
            'VerifyNarrowWindow(root, window, process.Id, Text("source"));',
            'WaitForPasteAvailability(false, "empty");',
            'WaitForPasteAvailability(false, "non-text");',
            'WaitForPasteAvailability(true, "text");',
            'new[] { "Connection configuration", "VPN connection action", "Profile 1 action", "Profile 2 action", "Backend logs" }',
            '[DllImport("user32.dll", SetLastError = true)]\n    private static extern bool SetWindowPos(',
            '((WindowPattern)windowPattern).Current.CanMaximize',
            '"Could not restore native window bounds after narrow-window test"',
            'originalBounds.Right - originalBounds.Left',
            'originalBounds.Bottom - originalBounds.Top',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, source)
        self.assertNotIn(".Current.CanResize", source)

    def test_msi_lifecycle_probes_registration_command_and_removal(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            package = root / "dobbyVPN-windows-amd64.msi"
            package.touch()
            runner = _RecordingRunner(root / "logs")
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
        self.assertIn("$scheme.GetValue('URL Protocol', $null)) { throw \"URL Protocol marker is missing\" }", installed_script)
        self.assertIn("$expected = '\"' + (Join-Path $root 'bin\\DobbyVPN.exe') + '\" \"%1\"'", installed_script)
        self.assertIn('if ($command -ne $expected) { throw "unexpected protocol command: $command" }', installed_script)
        self.assertEqual(runner.calls[0][1]["environment"]["DOBBYVPN_EXPECTED_VERSION"], "1.5.4")

        uninstalled_script = runner.calls[1][0][-1]
        self.assertIn("Test-Path 'HKLM:\\Software\\Classes\\dobbyvpn'", uninstalled_script)
        self.assertIn('DobbyVPN URL scheme remains registered after uninstall', uninstalled_script)


if __name__ == "__main__":
    unittest.main()
