from __future__ import annotations

import importlib.util
import json
import os
import subprocess
from pathlib import Path
import sys
import tempfile
import unittest
from types import SimpleNamespace
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
from torturer_runner import local_vm  # noqa: E402
from torturer_runner.ui import journey, smoke  # noqa: E402
from torturer_runner.native_cases import (  # noqa: E402
    MACOS_CONFIGURE_STARTUP_CASE,
    WINDOWS_CONFIGURE_TREE_CASE,
)

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
    def test_windows_cold_launch_uses_exact_interactive_child_process(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / "DobbyVPN.exe"
            helper = root / "native-ui.exe"
            profile = root / "source.url"
            binary.write_bytes(b"candidate")
            helper.write_bytes(b"helper")
            profile.write_text("https://example.invalid/subscription", encoding="utf-8")
            process = mock.Mock()
            process.pid = 2468
            process.poll.return_value = None
            controller = smoke.NativeUIController(
                "windows", binary, profile, 10.0,
                helper=helper, screenshot_dir=root / "screenshots",
            )
            probes = iter((
                {"alive": False},
                {"alive": True, "pid": 2468, "identity": "created-at-1"},
            ))

            def helper_call(operation: str, **_fields: object) -> dict[str, object]:
                if operation == "probe":
                    response = next(probes)
                    if response.get("alive") is True:
                        controller.pid = int(response["pid"])
                        controller.identity = str(response["identity"])
                    return response
                return {"ready": True}

            controller._call = mock.Mock(side_effect=helper_call)
            controller.snapshot = mock.Mock(return_value={
                "status": "Disconnected",
                "labels": ["Connection configuration"],
            })
            controller.capture = mock.Mock(return_value={"path": str(root / "startup.png")})
            with (
                mock.patch("torturer_runner.ui.smoke.subprocess.Popen", return_value=process) as popen,
                mock.patch("os.startfile", create=True) as startfile,
            ):
                result = controller.start()
            self.assertEqual(result["status"], "Disconnected")
            self.assertIs(controller.process, process)
            self.assertEqual(controller.pid, 2468)
            self.assertEqual(controller.identity, "created-at-1")
            self.assertEqual(popen.call_args.args[0], [str(binary)])
            self.assertIs(popen.call_args.kwargs["stdin"], subprocess.DEVNULL)
            self.assertTrue((root / "windows-app-01.stdout.log").is_file())
            self.assertTrue((root / "windows-app-01.stderr.log").is_file())
            startfile.assert_not_called()

    def test_auto_selection_requires_an_enabled_rendered_stop_before_connected(self) -> None:
        controller = object.__new__(smoke.NativeUIController)
        controller._timeout = 10.0
        controller._deadline = None
        controller._click = mock.Mock()
        controller.snapshot = mock.Mock(side_effect=(
            {
                "status": "Connecting",
                "labels": ["Stop", "Profile 1 action"],
                "enabled_controls": ["Stop"],
            },
            {
                "status": "Connected",
                "labels": ["Disconnect"],
                "enabled_controls": ["VPN connection action"],
            },
        ))

        with mock.patch("torturer_runner.ui.smoke.time.sleep"):
            result = controller.connect_with_auto_stop()

        self.assertTrue(result["auto_stop_observed"])
        controller._click.assert_called_once_with("VPN connection action")

    def test_auto_selection_does_not_claim_stop_after_it_has_already_connected(self) -> None:
        controller = object.__new__(smoke.NativeUIController)
        controller._timeout = 10.0
        controller._deadline = None
        controller._click = mock.Mock()
        controller.snapshot = mock.Mock(return_value={
            "status": "Connected",
            "labels": ["Disconnect"],
            "enabled_controls": ["VPN connection action"],
        })

        with self.assertRaisesRegex(smoke.NativeUISmokeError, "without a rendered, enabled Stop"):
            controller.connect_with_auto_stop()

    def test_warm_deep_link_requires_the_same_window_not_only_the_same_process(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "source.url"
            controller = object.__new__(smoke.NativeUIController)
            controller.platform = "windows"
            controller.profile = profile
            controller._timeout = 10.0
            controller._deadline = None
            controller._call = mock.Mock(side_effect=(
                {"pid": 42, "identity": "same-process", "windowHandle": "0x1234"},
                {"pid": 42, "identity": "same-process", "windowHandle": "0x5678"},
            ))
            controller._open_link = mock.Mock()
            controller._wait = mock.Mock(side_effect=lambda predicate, _message: self.assertTrue(predicate()))
            controller.snapshot = mock.Mock(return_value={"labels": ["Profile 1 action"]})

            with self.assertRaisesRegex(smoke.NativeUISmokeError, "existing Windows window"):
                controller.import_link("https://example.invalid/subscription")

            self.assertEqual(controller._open_link.call_count, 2)
            self.assertEqual(
                profile.read_text(encoding="utf-8"),
                "https://example.invalid/subscription",
            )

    def test_pending_import_dispatch_returns_without_waiting_for_profile_load(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "source.url"
            controller = object.__new__(smoke.NativeUIController)
            controller.platform = "windows"
            controller.profile = profile
            controller._timeout = 10.0
            controller._deadline = None
            controller._call = mock.Mock(side_effect=(
                {"pid": 42, "identity": "same-process", "windowHandle": "0x1234"},
                {"pid": 42, "identity": "same-process", "windowHandle": "0x1234"},
            ))
            controller._open_link = mock.Mock()
            controller._wait = mock.Mock(side_effect=AssertionError("pending import must not wait for load completion"))
            controller.snapshot = mock.Mock(side_effect=AssertionError("pending import must not wait for rendered profiles"))

            result = controller.dispatch_import_link("https://example.invalid/subscription?pending=1")
            saved_source = profile.read_text(encoding="utf-8")

        self.assertTrue(result["ready"])
        self.assertEqual(result["windowHandle"], "0x1234")
        controller._open_link.assert_called_once_with(
            "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fsubscription%3Fpending%3D1"
        )
        self.assertEqual(controller._call.call_count, 2)
        controller._wait.assert_not_called()
        controller.snapshot.assert_not_called()
        self.assertEqual(saved_source, "https://example.invalid/subscription?pending=1")

    def test_windows_probe_reports_main_window_handle_for_activation_checks(self) -> None:
        source = WINDOWS_NATIVE_UI.read_text(encoding="utf-8")
        self.assertIn("var probeWindowHandle = process.MainWindowHandle;", source)
        self.assertIn(
            'windowHandle = probeWindowHandle == IntPtr.Zero ? null : $"0x{probeWindowHandle.ToInt64():X}"',
            source,
        )

    def test_cold_deep_link_uses_windows_shell_only_when_ui_is_stopped(self) -> None:
        controller = object.__new__(smoke.NativeUIController)
        controller.platform = "windows"
        controller.process = None
        controller.launch_count = 1
        controller.window_id = "old-window"
        controller.last_window_readiness = {"old": True}
        controller._timeout = 10.0
        controller._deadline = None
        controller._alive = mock.Mock(return_value=False)
        controller._call = mock.Mock(return_value={"ready": True})
        controller._wait = mock.Mock(side_effect=lambda predicate, _message: self.assertTrue(predicate()))
        controller.snapshot = mock.Mock(return_value={"labels": ["Connection configuration"]})
        controller.wait_status = mock.Mock(return_value={"status": "Connected"})

        with mock.patch("os.startfile", create=True) as startfile:
            result = controller.cold_deep_link("https://example.invalid/subscription?source=one/two")

        self.assertEqual(result, {"status": "Connected"})
        startfile.assert_called_once_with(
            "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fsubscription%3Fsource%3Done%2Ftwo"
        )
        controller._call.assert_not_called()
        self.assertEqual(controller.launch_count, 2)
        self.assertIsNone(controller.window_id)
        self.assertIsNone(controller.last_window_readiness)

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
            'pasteInvokedAtUnixMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();',
            'paste_invoked_at_unix_ms = pasteInvokedAtUnixMs',
            'new[] { "Connection configuration", "VPN connection action", "Profile 1 action", "Profile 2 action", "Backend logs" }',
            '[DllImport("user32.dll", SetLastError = true)]\n    private static extern bool SetWindowPos(',
            '[DllImport("user32.dll")]\n    private static extern bool EnumThreadWindows(',
            'private static string[] DescribeProcessWindows(Process process)',
            '[DllImport("user32.dll")] private static extern bool ShowWindowAsync(IntPtr window, int command);',
            'tree-window-activation hwnd=0x',
            'if (traceTree)',
            'WaitFor(() =>',
            'return candidatePid == process.Id;',
            'return window != IntPtr.Zero && IsWindowVisible(window) && !IsIconic(window);',
            '"UI process did not expose a visible, non-minimized window for the tree snapshot", seconds: 20.0);',
            'TracePhase("tree-uia-root-complete")',
            'TracePhase("tree-uia-findall-start")',
            'var controlView = new PropertyCondition(\n                        AutomationElement.IsControlElementProperty,\n                        true);',
            'var foundElements = root.FindAll(TreeScope.Subtree, controlView);',
            'TracePhase($"tree-uia-findall-complete elements={foundElements.Count}");',
            'if (foundElements.Count > 8192)',
            'catch (COMException error) when (error.HResult == unchecked((int)0x8000FFFF))',
            'uiaError = error.ToString()',
            'var isOffscreen = current.IsOffscreen;',
            '((WindowPattern)windowPattern).Current.CanMaximize',
            '"Could not restore native window bounds after narrow-window test"',
            'originalBounds.Right - originalBounds.Left',
            'originalBounds.Bottom - originalBounds.Top',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, source)
        self.assertEqual(source.count("root.FindFirst(TreeScope.Subtree, new AndCondition("), 3)
        tree_operation = source.split('if (operation == "tree")', 1)[1].split(
            "long? pasteInvokedAtUnixMs", 1
        )[0]
        self.assertNotIn("Walk(root", tree_operation)
        for assertion in (
            'if (request.TryGetProperty("pid", out var requestedPid))',
            'Process.GetProcessesByName(Path.GetFileNameWithoutExtension(expected))',
            'throw new InvalidOperationException("More than one candidate UI process matches the executable")',
            'var identity = process.StartTime.ToUniversalTime().Ticks.ToString(CultureInfo.InvariantCulture);',
            'if (request.TryGetProperty("identity", out var prior) && prior.GetString() != identity)',
            'windowHandle = probeWindowHandle == IntPtr.Zero ? null : $"0x{probeWindowHandle.ToInt64():X}"',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, source)
        self.assertNotIn(".Current.CanResize", source)

    def test_tree_discovery_prefers_visible_owned_window_before_activation_fallback(self) -> None:
        source = WINDOWS_NATIVE_UI.read_text(encoding="utf-8")
        tree_discovery = source.split('if (activateAndDiscoverWindow)\n            {', 1)[1].split(
            '\n            else\n            {', 1
        )[0]

        owned_windows = tree_discovery.index("var ownedWindows = EnumerateProcessWindows(process).Where(candidate =>")
        ownership_filter = tree_discovery.index("return candidatePid == process.Id;", owned_windows)
        visible_choice = tree_discovery.index(
            "window = ownedWindows.FirstOrDefault(candidate =>", ownership_filter
        )
        fallback_choice = tree_discovery.index(
            "window = ownedWindows.FirstOrDefault();", visible_choice
        )
        self.assertLess(owned_windows, ownership_filter)
        self.assertLess(ownership_filter, visible_choice)
        self.assertIn("IsWindowVisible(candidate) && !IsIconic(candidate)", tree_discovery[visible_choice:fallback_choice])
        self.assertLess(visible_choice, fallback_choice)

    def test_native_ui_helper_reports_log_scroll_position(self) -> None:
        source = WINDOWS_NATIVE_UI.read_text(encoding="utf-8")

        for assertion in (
            'if (operation == "log-position")',
            'Find("Backend logs")',
            'scroll.Current.VerticalScrollPercent',
            'vertical_scroll_percent = position',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, source)

    def test_windows_paste_controller_retains_helper_invocation_time(self) -> None:
        controller = object.__new__(smoke.NativeUIController)
        controller.platform = "windows"
        controller.profile = Path("C:/run/source.url")
        controller._call = mock.Mock(return_value={
            "ready": True,
            "paste_invoked_at_unix_ms": 123456,
        })
        controller.snapshot = mock.Mock(return_value={"labels": ["Profile 1 action"]})

        result = controller.paste_source()

        self.assertEqual(result, {"labels": ["Profile 1 action"]})
        self.assertEqual(controller.last_paste_invoked_at_unix_ms, 123456)
        controller._call.assert_called_once_with("paste", source="C:/run/source.url")

    def test_windows_native_case_command_selects_configure_tree(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            run_dir = root / "run"
            source_root = run_dir / "source"
            source = source_root / "torturer" / "torturer_runner" / "ui"
            source.mkdir(parents=True)
            (source_root / "VERSION").write_text("1.5.4\n", encoding="utf-8")
            (source / "smoke.py").touch()
            (source / "journey.py").touch()
            helper = root / "NativeUI.exe"
            helper.touch()
            selected = "configure-tree"
            command = local_vm._native_ui_command(
                run_dir,
                {"cli": "cli.exe", "ui": "ui.exe"},
                {"pid": 42, "binary": "service.exe", "pipe": "DobbyVPN.Control"},
                "windows",
                30,
                helper,
                native_cases=(selected,),
                source_sha="a" * 40,
            )
            self.assertEqual(
                [command[index + 1] for index, value in enumerate(command[:-1]) if value == "--native-case"],
                [selected],
            )
            self.assertEqual(command[command.index("--candidate-version") + 1], "1.5.4")
            self.assertEqual(command[command.index("--source-sha") + 1], "a" * 40)

    def test_windows_tree_helper_allows_bounded_window_discovery(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            helper = root / "NativeUI.exe"
            helper.touch()
            controller = smoke.NativeUIController(
                "windows",
                root / "DobbyVPN.exe",
                root / "profile.txt",
                60,
                helper=helper,
                screenshot_dir=root / "screenshots",
            )
            completed = subprocess.CompletedProcess(
                [str(helper)], 0, b'{"ready":true}', b""
            )
            with (
                mock.patch.object(
                    smoke, "_windows_job_capture_callbacks",
                    return_value=(mock.Mock(), mock.Mock(), mock.Mock()),
                ),
                mock.patch.object(smoke, "_native_run", return_value=completed) as run,
            ):
                controller._call("tree")

        self.assertEqual(run.call_args.kwargs["timeout_seconds"], 30.0)
        request = json.loads(run.call_args.kwargs["input_bytes"])
        self.assertEqual(request["operation"], "tree")

    def test_windows_about_matches_candidate_version_and_source_sha(self) -> None:
        commit = "a" * 40
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            helper = root / "NativeUI.exe"
            helper.touch()
            controller = smoke.NativeUIController(
                "windows",
                root / "DobbyVPN.exe",
                root / "profile.txt",
                30,
                helper=helper,
                screenshot_dir=root / "screenshots",
                expected_version="1.5.4",
                expected_source_sha=commit,
            )
            source_url = f"https://github.com/DobbyVPN/DobbyVPN/tree/{commit}"
            snapshots = iter((
                {
                    "labels": [
                        "Version: 1.5.4", f"Commit: {commit[:12]}",
                        f"Source commit: {commit}", "About source link",
                    ],
                    "help_texts": [source_url],
                },
                {"labels": ["Connection configuration"]},
            ))
            controller._click = mock.Mock()
            controller._wait = lambda predicate, message: self.assertTrue(predicate(), message)
            controller.snapshot = mock.Mock(side_effect=lambda: next(snapshots))
            controller.capture = mock.Mock(return_value={})

            self.assertEqual(
                controller.about(),
                {"about_version": True, "about_source_commit": True},
            )

    def test_native_case_driver_runs_configure_tree_and_macos_startup(self) -> None:
        class FakeController:
            def __init__(self, *_args, **_kwargs):
                self.operations: list[str] = []

            @staticmethod
            def bounded_by(_timeout):
                return mock.MagicMock(__enter__=mock.Mock(), __exit__=mock.Mock(return_value=False))

            def start(self):
                self.operations.append("start-tree")
                return {"status": "Disconnected", "labels": ["Connection configuration"]}

            def configure(self):
                self.operations.append("rendered-configure")
                return {"input_verified": True, "labels": ["Profile 1 action"]}

            def close_for_cleanup(self):
                self.operations.append("close")

            def collect_diagnostics(self):
                self.operations.append("collect")

        class FakeSubscriptionFixture:
            def __init__(self, profile, directory, platform, *, certificate_helper):
                self.profile = profile
                self.directory = directory
                self.platform = platform
                self.certificate_helper = certificate_helper

            def start(self):
                self.directory.mkdir(parents=True)
                return "https://127.0.0.1:49152/subscription"

            @staticmethod
            def control_stats():
                return {
                    "subscription_gets": 1,
                    "in_flight_gets": 0,
                    "max_in_flight_gets": 1,
                }

            @staticmethod
            def close():
                pass

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile"
            profile.write_bytes(b"disposable profile bytes")

            def run_case(platform: str, case: str) -> tuple[dict, FakeController]:
                constructed: list[FakeController] = []

                def factory(*args, **kwargs):
                    value = FakeController(*args, **kwargs)
                    constructed.append(value)
                    return value

                with (
                    mock.patch.object(journey.smoke, "NativeUIController", side_effect=factory),
                    mock.patch(
                        "torturer_runner.subscription_fixture.SubscriptionFixture",
                        FakeSubscriptionFixture,
                    ),
                ):
                    result = journey.run_native_cases(SimpleNamespace(
                        platform=platform,
                        native_cases=[case],
                        raw_log_dir=root / "logs",
                        ui=Path("app"),
                        profile=profile,
                        timeout=30,
                        ui_helper=Path("helper"),
                    ))
                return result, constructed[0]

            windows, windows_controller = run_case("windows", WINDOWS_CONFIGURE_TREE_CASE)
            macos, macos_controller = run_case("macos", MACOS_CONFIGURE_STARTUP_CASE)
        self.assertEqual(windows["coverage"]["native_cases"], [WINDOWS_CONFIGURE_TREE_CASE])
        self.assertEqual(windows_controller.operations, ["start-tree", "close", "collect"])
        self.assertEqual(macos["checks"][MACOS_CONFIGURE_STARTUP_CASE]["configure"]["input_verified"], True)
        self.assertEqual(macos_controller.operations, ["start-tree", "rendered-configure", "close", "collect"])

    def test_windows_native_ui_retains_window_readiness_diagnostics(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            helper = root / "NativeUI.exe"
            helper.touch()
            controller = smoke.NativeUIController(
                "windows",
                root / "DobbyVPN.exe",
                root / "profile.txt",
                5,
                helper=helper,
                screenshot_dir=root / "screenshots",
            )
            controller.pid = 42
            controller.identity = "candidate-ui-instance"
            response = {
                "ready": False,
                "pid": 42,
                "identity": "candidate-ui-instance",
                "windowHandle": "0x0",
                "visible": False,
                "minimized": False,
                "ownerPid": 0,
                "candidateSessionId": 1,
                "helperSessionId": 1,
                "mainWindowTitle": "",
                "windowDescription": "unavailable",
                "processTopLevelWindows": [],
                "uiaError": "System.Runtime.InteropServices.COMException (0x8000FFFF): Catastrophic failure",
            }
            completed = subprocess.CompletedProcess(
                [str(helper)], 0, json.dumps(response).encode("utf-8"), b""
            )

            with mock.patch.object(smoke, "_native_run", return_value=completed):
                snapshot = controller.snapshot()

        self.assertEqual(snapshot["status"], "Unknown")
        self.assertEqual(
            controller.last_window_readiness,
            {key: value for key, value in response.items() if key not in {"ready", "pid", "identity"}},
        )

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
                response = json.dumps({
                    "pid": 42, "identity": "existing-ui-instance", "windowHandle": "0x1234"
                }).encode("utf-8")
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
