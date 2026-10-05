from __future__ import annotations

import importlib.util
import json
import subprocess
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


PRODUCT_ROOT = Path(__file__).resolve().parents[4]
WINDOWS_PROGRAM = PRODUCT_ROOT / "ui/windows/DobbyVPN.Windows/Program.cs"
WINDOWS_NATIVE_UI = PRODUCT_ROOT / "torturer/native_ui/windows/Program.cs"
WINDOWS_COMPONENTS = PRODUCT_ROOT / "ui/windows/installer/AppComponents.wxs"
MIGRATION_PATH = PRODUCT_ROOT / ".github/scripts/desktop/installer_migration.py"
WINDOWS_SERVICE_EXECUTOR = PRODUCT_ROOT / "core/clientserver/executor/windows_execute.go"
DESKTOP_SHUTDOWN = PRODUCT_ROOT / "core/clientserver/executor/process.go"
DESKTOP_BINDING = PRODUCT_ROOT / "core/sessionapi/mobilebinding/binding.go"
SESSION_SHUTDOWN = PRODUCT_ROOT / "core/sessionapi/shutdown.go"
DESKTOP_SHUTDOWN_TEST = PRODUCT_ROOT / "core/clientserver/executor/process_test.go"
TORTURER_ROOT = PRODUCT_ROOT / "torturer"
if str(TORTURER_ROOT) not in sys.path:
    sys.path.insert(0, str(TORTURER_ROOT))
from torturer_runner.ui import smoke  # noqa: E402

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

        for assertion in (
            'AppInstance.GetCurrent().GetActivatedEventArgs()',
            'AppInstance.FindOrRegisterForKey("DobbyVPN")',
            'if (!instance.IsCurrent)',
            'await instance.RedirectActivationToAsync(activation);',
            'instance.Activated += (_, next) =>',
            'Pending.Enqueue(next);',
            '_window?.DispatcherQueue.TryEnqueue(Drain);',
            'activation.Kind == ExtendedActivationKind.Protocol',
            '_window.ImportLink(protocol.Uri.AbsoluteUri);',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, source)
        self.assertIn("public static async Task Main(string[] args)", source)
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
        for assertion in (
            'if (request.TryGetProperty("pid", out var requestedPid))',
            'Process.GetProcessesByName(Path.GetFileNameWithoutExtension(expected))',
            'throw new InvalidOperationException("More than one candidate UI process matches the executable")',
            'var identity = process.StartTime.ToUniversalTime().Ticks.ToString(CultureInfo.InvariantCulture);',
            'if (request.TryGetProperty("identity", out var prior) && prior.GetString() != identity)',
            'new { alive = true, pid = process.Id, identity }',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, source)
        self.assertNotIn(".Current.CanResize", source)

    def test_warm_protocol_import_uses_shell_and_keeps_the_existing_ui_process(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            helper = root / "NativeUI.exe"
            helper.touch()
            profile = root / "profile.txt"
            controller = smoke.NativeUIController(
                "windows",
                root / "DobbyVPN.exe",
                profile,
                5,
                helper=helper,
                screenshot_dir=root / "screenshots",
            )
            controller.pid = 42
            controller.identity = "existing-ui-instance"
            helper_requests: list[dict[str, object]] = []

            def run_helper(command: list[str], **kwargs: object) -> subprocess.CompletedProcess[bytes]:
                helper_requests.append(json.loads(kwargs["input_bytes"]))
                response = json.dumps({"pid": 42, "identity": "existing-ui-instance"}).encode("utf-8")
                return subprocess.CompletedProcess(command, 0, response, b"")

            with mock.patch.object(smoke, "_native_run", side_effect=run_helper), \
                 mock.patch.object(controller, "snapshot", return_value={"labels": ["Profile 1 action"]}), \
                 mock.patch.object(smoke.os, "startfile", create=True) as shell_open, \
                 mock.patch.object(smoke.time, "sleep"):
                view = controller.import_link("https://example.invalid/subscription?source=warm")

        self.assertEqual(view, {"labels": ["Profile 1 action"]})
        self.assertEqual(shell_open.call_count, 2)
        opened_links = [call.args[0] for call in shell_open.call_args_list]
        self.assertEqual(opened_links[0], opened_links[1])
        self.assertEqual(opened_links[0], "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fsubscription%3Fsource%3Dwarm")
        self.assertEqual(helper_requests[0]["operation"], "probe")
        self.assertEqual(helper_requests[0]["pid"], 42)
        self.assertEqual(helper_requests[0]["identity"], "existing-ui-instance")
        self.assertEqual(helper_requests[1]["operation"], "probe")
        self.assertNotIn("pid", helper_requests[1])
        self.assertNotIn("identity", helper_requests[1])

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

    def test_windows_service_shutdown_routes_to_the_shared_session_owner(self) -> None:
        service = WINDOWS_SERVICE_EXECUTOR.read_text(encoding="utf-8")
        desktop = DESKTOP_SHUTDOWN.read_text(encoding="utf-8")
        binding = DESKTOP_BINDING.read_text(encoding="utf-8")
        owner = SESSION_SHUTDOWN.read_text(encoding="utf-8")
        shutdown_test = DESKTOP_SHUTDOWN_TEST.read_text(encoding="utf-8")

        self.assertIn("svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown", service)
        self.assertIn("request.Cmd == svc.Stop || request.Cmd == svc.Shutdown || request.Cmd == svc.PreShutdown", service)
        self.assertIn("shutdownDesktop(stopControl, serveErr, desktopProcessBinding())", service)
        self.assertIn("owner.StopAndWait(context.Background())", desktop)
        self.assertIn("return owner.StopAndWait(ctx)", binding)
        self.assertIn("s.pending = nil", owner)
        self.assertIn("if s.cancel != nil {\n\t\t\ts.cancel()", owner)
        self.assertIn("func TestDesktopShutdownOwnerCancelsPendingSwitch", shutdown_test)


if __name__ == "__main__":
    unittest.main()
