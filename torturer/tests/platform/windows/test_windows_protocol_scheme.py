from __future__ import annotations

import base64
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
WINDOWS_MAIN_WINDOW = PRODUCT_ROOT / "ui/windows/DobbyVPN.Windows/MainWindow.xaml.cs"
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
    WINDOWS_CONFIGURE_TREE_CASE,
    WINDOWS_CONFIGURE_TREE_NO_UIA_CASE,
)

SPEC = importlib.util.spec_from_file_location("dobbyvpn_installer_migration_test", MIGRATION_PATH)
if SPEC is None or SPEC.loader is None:
    raise AssertionError(f"could not load {MIGRATION_PATH}")
installer_migration = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = installer_migration
SPEC.loader.exec_module(installer_migration)


def _windows_content_root_probe() -> dict[str, object]:
    return {
        "schema": "dobbyvpn.windows-content-root-peers/v2",
        "completed": True,
        "diagnosticOnly": True,
        "dispatcherThreadAccess": True,
        "windowContentIsRoot": True,
        "windowContentType": "Microsoft.UI.Xaml.Controls.Grid",
        "rootPeerCreated": False,
        "rootPeerType": None,
        "editor": {
            "automationId": "Connection configuration",
            "name": "Subscription URL",
            "controlType": "Edit",
            "peerType": "Microsoft.UI.Xaml.Automation.Peers.TextBoxAutomationPeer",
            "isControlElement": True,
            "isContentElement": True,
            "isLoaded": True,
            "isVisible": True,
            "isEnabled": True,
            "geometry": {"x": 24.0, "y": 84.0, "width": 380.0, "height": 40.0},
            "screenGeometry": {
                "coordinateSpace": "physical-screen-pixels",
                "clientOriginDpiContext": "per-monitor-v2",
                "clientOrigin": {"x": 100, "y": 200},
                "rasterizationScale": 1.5,
                "left": 136.0,
                "top": 326.0,
                "width": 570.0,
                "height": 60.0,
                "centerX": 421,
                "centerY": 356,
            },
            "screenGeometryError": None,
        },
    }


class _RecordingRunner:
    def __init__(self, log_dir: Path) -> None:
        self.log_dir = log_dir
        self.calls: list[tuple[list[str], dict[str, object]]] = []

    def run(self, command: list[str], **kwargs: object) -> None:
        self.calls.append((command, kwargs))


class _FakeRegistryKey:
    def __init__(self, registry: "_FakeRegistry", path: str) -> None:
        self.registry = registry
        self.path = path

    def __enter__(self) -> "_FakeRegistryKey":
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def Close(self) -> None:
        return None


class _FakeRegistry:
    HKEY_LOCAL_MACHINE = object()
    KEY_READ = 1
    KEY_WRITE = 2
    KEY_WOW64_64KEY = 4
    REG_DWORD = 4
    REG_EXPAND_SZ = 2
    REG_SZ = 1

    def __init__(self) -> None:
        self.values: dict[str, dict[str, tuple[object, int]]] = {}

    def _ensure(self, path: str) -> None:
        current = ""
        for component in path.split("\\"):
            current = component if not current else current + "\\" + component
            self.values.setdefault(current, {})

    def OpenKey(self, _hive: object, path: str, _reserved: int, _access: int) -> _FakeRegistryKey:
        if path not in self.values:
            raise FileNotFoundError(path)
        return _FakeRegistryKey(self, path)

    def CreateKeyEx(
        self,
        _hive: object,
        path: str,
        _reserved: int,
        _access: int,
    ) -> _FakeRegistryKey:
        self._ensure(path)
        return _FakeRegistryKey(self, path)

    def QueryValueEx(self, key: _FakeRegistryKey, name: str) -> tuple[object, int]:
        try:
            return self.values[key.path][name]
        except KeyError as error:
            raise FileNotFoundError(name) from error

    def SetValueEx(
        self,
        key: _FakeRegistryKey,
        name: str,
        _reserved: int,
        value_type: int,
        value: object,
    ) -> None:
        self.values[key.path][name] = (value, value_type)

    def DeleteValue(self, key: _FakeRegistryKey, name: str) -> None:
        try:
            del self.values[key.path][name]
        except KeyError as error:
            raise FileNotFoundError(name) from error

    def DeleteKeyEx(
        self,
        _hive: object,
        path: str,
        _access: int,
        _reserved: int,
    ) -> None:
        if path not in self.values:
            raise FileNotFoundError(path)
        if any(candidate.startswith(path + "\\") for candidate in self.values):
            raise OSError("registry key has children")
        del self.values[path]


class WindowsProtocolSchemeTests(unittest.TestCase):
    def test_configure_tree_requires_completed_direct_editor_metadata(self) -> None:
        valid = _windows_content_root_probe()
        self.assertTrue(journey._valid_windows_content_root_probe(valid))

        invalid_probes = []
        wrong_schema = json.loads(json.dumps(valid))
        wrong_schema["schema"] = "dobbyvpn.windows-content-root-peers/v1"
        invalid_probes.append(wrong_schema)
        missing_editor = json.loads(json.dumps(valid))
        del missing_editor["editor"]
        invalid_probes.append(missing_editor)
        invisible_editor = json.loads(json.dumps(valid))
        invisible_editor["editor"]["isVisible"] = False
        invalid_probes.append(invisible_editor)
        empty_geometry = json.loads(json.dumps(valid))
        empty_geometry["editor"]["geometry"]["width"] = 0
        invalid_probes.append(empty_geometry)
        inconsistent_root_peer = json.loads(json.dumps(valid))
        inconsistent_root_peer["rootPeerCreated"] = True
        invalid_probes.append(inconsistent_root_peer)

        for probe in invalid_probes:
            with self.subTest(probe=probe):
                self.assertFalse(journey._valid_windows_content_root_probe(probe))

    def test_windows_content_root_diagnostic_start_does_not_snapshot_or_capture(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / "DobbyVPN.exe"
            helper = root / "NativeUI.exe"
            binary.touch()
            helper.touch()
            controller = smoke.NativeUIController(
                "windows",
                binary,
                root / "profile.txt",
                30,
                helper=helper,
                screenshot_dir=root / "screenshots",
            )
            controller._alive = mock.Mock(return_value=False)
            controller._wait = lambda predicate, message: self.assertTrue(predicate(), message)
            controller.snapshot = mock.Mock(side_effect=AssertionError("probe-only start called Snapshot"))
            controller.capture = mock.Mock(side_effect=AssertionError("probe-only start captured the UI"))
            operations: list[str] = []
            root_peer_result = _windows_content_root_probe()
            settings_result = {
                "ready": False,
                "available": False,
                "error": "Text size slider is unavailable",
                "cleanupErrors": ["no SystemSettings-owned window was available"],
            }

            def call(operation: str, **_fields: object) -> dict[str, object]:
                operations.append(operation)
                if operation == "windows-baseline":
                    return {"ready": True, "pid": 42, "windowHandle": "0x100"}
                if operation == "settings-text-size":
                    return settings_result
                controller.pid = 42
                controller.identity = "candidate-ui-instance"
                if operation == "uia-point":
                    return {"ready": True, "targetMetadataCompleted": True}
                return {"alive": True, "pid": 42, "identity": controller.identity}

            controller._call = call
            process = mock.Mock(pid=42)
            process.poll.return_value = None

            def launch(*_args, **kwargs):
                Path(kwargs["env"]["DOBBYVPN_NATIVE_UI_CONTENT_ROOT_PEERS_PATH"]).write_text(
                    json.dumps(root_peer_result), encoding="utf-8"
                )
                return process

            with mock.patch.object(smoke.subprocess, "Popen", side_effect=launch):
                result = controller.start(
                    windows_content_root_diagnostics=True,
                )

            self.assertEqual(
                operations,
                ["probe", "windows-baseline", "uia-point", "settings-text-size", "probe"],
            )
            self.assertEqual(result, controller.windows_content_root_diagnostics)
            self.assertEqual(result["xaml_content_root_peers"], root_peer_result)
            self.assertEqual(result["windows_text_size_settings"], settings_result)
            self.assertEqual(result["post_probe_process"]["alive"], True)
            controller.snapshot.assert_not_called()
            controller.capture.assert_not_called()

    def test_windows_content_root_point_failure_keeps_full_exception_and_post_probe(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            controller = object.__new__(smoke.NativeUIController)
            controller.logs = Path(directory)
            controller.platform = "windows"
            controller._timeout = 5.0
            controller._deadline = None
            controller.process = None
            point_stdout = b"NativeUI stdout: \xff"
            point_stderr = b"FromPoint provider failure\r\n"
            (controller.logs / "windows-content-root-peers.json").write_text(
                json.dumps(_windows_content_root_probe()), encoding="utf-8"
            )
            controller._call = mock.Mock(side_effect=(
                {"ready": True, "pid": 42, "windowHandle": "0x100"},
                subprocess.CalledProcessError(
                    17,
                    "FromPoint COM call",
                    output=point_stdout,
                    stderr=point_stderr,
                ),
                subprocess.CalledProcessError(
                    18,
                    "Settings inspect",
                    output=b"Settings helper stdout\n",
                    stderr=b"no owned window\xff",
                ),
                {"alive": True, "pid": 42},
            ))

            result = controller._run_windows_content_root_diagnostics()

            self.assertEqual(
                [call.args[0] for call in controller._call.call_args_list],
                ["windows-baseline", "uia-point", "settings-text-size", "probe"],
            )
            self.assertIn(
                "FromPoint COM call",
                result["external_uia_point_exception"],
            )
            self.assertIn("Traceback", result["external_uia_point_exception"])
            self.assertEqual(
                result["external_uia_point_stdout_base64"],
                base64.b64encode(point_stdout).decode("ascii"),
            )
            self.assertEqual(
                result["external_uia_point_stderr_base64"],
                base64.b64encode(point_stderr).decode("ascii"),
            )
            self.assertIn("Settings inspect", result["windows_text_size_settings_exception"])
            self.assertEqual(
                result["windows_text_size_settings_stdout_base64"],
                base64.b64encode(b"Settings helper stdout\n").decode("ascii"),
            )
            self.assertEqual(
                result["windows_text_size_settings_stderr_base64"],
                base64.b64encode(b"no owned window\xff").decode("ascii"),
            )
            self.assertEqual(result["post_probe_process"]["alive"], True)

    def test_windows_local_dumps_restores_existing_values_and_removes_new_keys(self) -> None:
        registry = _FakeRegistry()
        existing_path = smoke._WINDOWS_LOCAL_DUMPS_PATH + r"\DobbyVPN.exe"
        retained_path = smoke._WINDOWS_LOCAL_DUMPS_PATH + r"\Unrelated.exe"
        registry._ensure(existing_path)
        registry._ensure(retained_path)
        registry.values[existing_path].update({
            "DumpFolder": (r"C:\prior\dumps", registry.REG_SZ),
            "DumpCount": (7, registry.REG_DWORD),
            "UnrelatedValue": ("keep", registry.REG_SZ),
        })
        registry.values[retained_path]["DumpType"] = (2, registry.REG_DWORD)
        before = {path: dict(values) for path, values in registry.values.items()}

        local_dumps = smoke._WindowsLocalDumps(registry)
        local_dumps.enable(
            ("DobbyVPN.exe", "NativeUI.exe"),
            Path(r"C:\run\logs\windows-wer-dumps"),
        )

        self.assertEqual(
            registry.values[existing_path]["DumpFolder"],
            (r"C:\run\logs\windows-wer-dumps", registry.REG_EXPAND_SZ),
        )
        self.assertEqual(registry.values[existing_path]["DumpType"], (1, registry.REG_DWORD))
        self.assertEqual(registry.values[existing_path]["DumpCount"], (1, registry.REG_DWORD))
        self.assertIn(smoke._WINDOWS_LOCAL_DUMPS_PATH + r"\NativeUI.exe", registry.values)

        local_dumps.restore()

        self.assertEqual(registry.values, before)

    def test_windows_local_dumps_removes_keys_it_created(self) -> None:
        registry = _FakeRegistry()
        registry._ensure(r"SOFTWARE\Microsoft\Windows")
        before = {path: dict(values) for path, values in registry.values.items()}
        local_dumps = smoke._WindowsLocalDumps(registry)

        local_dumps.enable(("DobbyVPN.exe",), Path(r"C:\run\dumps"))
        local_dumps.restore()

        self.assertEqual(registry.values, before)

    def test_windows_configure_tree_diagnostic_uses_xaml_root_peers(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            helper_source = WINDOWS_NATIVE_UI.read_text(encoding="utf-8")
            app_source = WINDOWS_MAIN_WINDOW.read_text(encoding="utf-8")
            smoke_source = (TORTURER_ROOT / "torturer_runner/ui/smoke.py").read_text(encoding="utf-8")

            self.assertIn("Root.Loaded += (_, _) => WriteContentRootPeerDiagnostic()", app_source)
            self.assertIn("DispatcherQueue.HasThreadAccess", app_source)
            self.assertIn("FrameworkElementAutomationPeer.CreatePeerForElement(SourceEditor)", app_source)
            self.assertIn('schema = "dobbyvpn.windows-content-root-peers/v2"', app_source)
            self.assertIn("automationId = editorPeer.GetAutomationId()", app_source)
            self.assertIn("controlType = editorPeer.GetAutomationControlType().ToString()", app_source)
            self.assertIn("windowContentIsRoot = ReferenceEquals(content, Root)", app_source)
            self.assertIn("geometry = new", app_source)
            self.assertIn("SourceEditor.TransformToVisual(null)", app_source)
            self.assertIn("SourceEditor.XamlRoot", app_source)
            self.assertIn("xamlRoot.RasterizationScale", app_source)
            self.assertIn("GetPhysicalClientOrigin(windowHandle)", app_source)
            self.assertIn("SetThreadDpiAwarenessContext(new IntPtr(-4))", app_source)
            self.assertIn("ClientToScreen(window, ref point)", app_source)
            self.assertNotIn("GetChildren()", app_source)
            self.assertNotIn("immediateChildren", app_source)
            self.assertIn('app_environment["DOBBYVPN_NATIVE_UI_CONTENT_ROOT_PEERS_PATH"]', smoke_source)
            self.assertNotIn("uia-inputsite-sibling", helper_source)
            self.assertNotIn("GetNextSibling(targetPane)", helper_source)
            self.assertNotIn("InputSiteWindowClass", helper_source + app_source)
            self.assertIn("return Walk(root, trace).FirstOrDefault(element =>", helper_source)
            self.assertIn("walker.GetFirstChild(element)", helper_source)
            self.assertIn("walker.GetNextSibling(child)", helper_source)
            point_start = helper_source.index("private static int MeasureAutomationElementFromPoint(")
            point_end = helper_source.index("private static Rectangle Capture(", point_start)
            point_method = helper_source[point_start:point_end]
            self.assertIn('request.TryGetProperty("clientApi", out var clientApiElement)', point_method)
            self.assertIn(': "managed";', point_method)
            self.assertEqual(point_method.count("AutomationElement.FromPoint("), 1)
            self.assertEqual(point_method.count("comAutomation!.ElementFromPoint("), 1)
            self.assertIn("CapturePointContextBeforeFromPoint(window, pointX, pointY)", point_method)
            point_query = point_method.index("comAutomation!.ElementFromPoint(")
            self.assertEqual(point_method.count("ProbeWindowMessageResponsiveness("), 2)
            self.assertLess(point_method.index('response["windowMessageBeforeFromPoint"]'), point_query)
            after_probe = point_method.index('response["windowMessageAfterFromPoint"]')
            self.assertLess(point_query, after_probe)
            self.assertIn("finally", point_method[point_query:after_probe])
            failure_dump_gate = point_method.index(
                "if (failedHresult || response.ContainsKey(\"fromPointException\"))"
            )
            self.assertGreater(failure_dump_gate, after_probe)
            self.assertEqual(point_method.count("CapturePointFailureDump("), 1)
            dump_method_start = helper_source.index(
                "private static Dictionary<string, object?> CapturePointFailureDump("
            )
            dump_method_end = helper_source.index(
                "private static AutomationElement RequireAutomationId(", dump_method_start
            )
            dump_method = helper_source[dump_method_start:dump_method_end]
            self.assertIn("MiniDumpWithFullMemory | MiniDumpWithThreadInfo", dump_method)
            self.assertIn("actualIdentity != expectedIdentity", dump_method)
            self.assertIn("MiniDumpWriteDump(", dump_method)
            self.assertIn('result["miniDumpWriteDumpLastError"]', dump_method)
            self.assertIn('result["partialBytes"]', dump_method)
            message_probe_start = helper_source.index(
                "private static Dictionary<string, object?> ProbeWindowMessageResponsiveness("
            )
            message_probe = helper_source[message_probe_start:helper_source.index(
                "private static AutomationElement RequireAutomationId(", message_probe_start)]
            self.assertTrue(all(fragment in message_probe for fragment in (
                "DescribeWindowContext(window, includeThreadDesktop: false, includeGeometry: false)",
                "SendMessageTimeout(", "WmNull", "SmtoAbortIfHung | SmtoErrorOnExit",
                "const uint timeoutMs = 1000", "owner.StartTime.ToUniversalTime().Ticks",
            )))
            self.assertNotIn("PostMessage(", message_probe)
            self.assertIn('response["windowMessageBeforeFromPoint"]', point_method)
            self.assertIn('response["windowMessageAfterFromPoint"]', point_method)
            self.assertNotIn("windowMessageContinuity", point_method)
            self.assertLess(
                point_method.index("CapturePointContextBeforeFromPoint(window, pointX, pointY)"),
                min(
                    point_method.index("AutomationElement.FromPoint(new"),
                    point_method.index("comAutomation!.ElementFromPoint("),
                ),
            )
            self.assertIn('response["pointContextBeforeFromPoint"]', point_method)
            self.assertIn('response["clientApi"] = clientApi', point_method)
            self.assertIn('response["fromPointHresult"]', point_method)
            self.assertIn('response["targetMetadataClientApi"] = "com"', point_method)
            self.assertIn('ReleaseComObject(comTarget, "comTargetReleaseRemainingReferences", response)', point_method)
            self.assertIn("walker.GetParent(", point_method)
            self.assertIn("maximumAncestors\"] = 8", point_method)
            self.assertNotIn("GetFirstChild", point_method)
            self.assertNotIn("GetNextSibling", point_method)
            com_metadata_start = helper_source.index(
                "private static void CaptureComPointTargetMetadata("
            )
            com_metadata_end = helper_source.index(
                "private static IntPtr ParseWindowHandle(", com_metadata_start
            )
            com_metadata_method = helper_source[com_metadata_start:com_metadata_end]
            self.assertNotIn("TreeWalker", com_metadata_method)
            self.assertNotIn("FindFirst(", com_metadata_method)
            self.assertIn("UiaProcessIdPropertyId = 30002", helper_source)
            self.assertIn("UiaControlTypePropertyId = 30003", helper_source)
            self.assertIn("UiaNamePropertyId = 30005", helper_source)
            self.assertIn("UiaAutomationIdPropertyId = 30011", helper_source)
            self.assertIn("UiaBoundingRectanglePropertyId = 30001", helper_source)
            self.assertIn("UiaIsOffscreenPropertyId = 30022", helper_source)
            context_start = helper_source.index(
                "private static Dictionary<string, object?> CapturePointContextBeforeFromPoint("
            )
            context_end = helper_source.index("private static AutomationElement RequireAutomationId(", context_start)
            point_context = helper_source[context_start:context_end]
            self.assertIn("return InPerMonitorV2DpiContext(() =>", point_context)
            self.assertIn("WindowFromPoint(new NativePoint { X = pointX, Y = pointY })", point_context)
            main_start = helper_source.index("private static int Main(string[] args)")
            point_dispatch_start = helper_source.index(
                "if (traceAutomationPoint)\n            {", main_start
            )
            point_dispatch_end = helper_source.index("if (traceTree)", point_dispatch_start)
            point_dispatch = helper_source[point_dispatch_start:point_dispatch_end]
            self.assertIn("new Thread(() =>", point_dispatch)
            self.assertIn("pointQueryThread.SetApartmentState(ApartmentState.MTA)", point_dispatch)
            self.assertIn("pointQueryThread.Start()", point_dispatch)
            self.assertIn("pointQueryThread.Join()", point_dispatch)
            self.assertIn("pointQueryError = ExceptionDispatchInfo.Capture(error)", point_dispatch)
            self.assertIn("pointQueryError?.Throw()", point_dispatch)
            self.assertEqual(point_dispatch.count("MeasureAutomationElementFromPoint("), 1)
            self.assertIn(
                '["clientApartmentState"] = Thread.CurrentThread.GetApartmentState().ToString()',
                point_method,
            )
            self.assertIn('request.GetProperty("windowHandle")', helper_source)
            self.assertIn('self._call(\n                        "uia-point"', smoke_source)
            self.assertIn('clientApi="com"', smoke_source)

            controller = object.__new__(smoke.NativeUIController)
            controller.logs = Path(directory)
            controller._timeout = 5.0
            controller._deadline = None
            controller.process = None
            controller._windows_wer_dump_dir = Path(directory) / "windows-wer-dumps"
            controller._windows_wer_dump_dir.mkdir()
            root_peer_result = _windows_content_root_probe()
            (controller.logs / "windows-content-root-peers.json").write_text(
                json.dumps(root_peer_result), encoding="utf-8"
            )
            controller._call = mock.Mock(side_effect=(
                {"ready": True, "pid": 42, "windowHandle": "0x100"},
                {"ready": True, "completed": True, "targetMetadataCompleted": True},
                {"alive": True, "pid": 42},
            ))

            result = controller._run_windows_content_root_diagnostics()

            self.assertEqual(
                [call.args[0] for call in controller._call.call_args_list],
                ["windows-baseline", "uia-point", "probe"],
            )
            point_request = controller._call.call_args_list[1]
            self.assertEqual(
                point_request.kwargs,
                {
                    "clientApi": "com",
                    "windowHandle": "0x100",
                    "x": 421,
                    "y": 356,
                    "clientOriginX": 100,
                    "clientOriginY": 200,
                    "expectedAutomationId": "Connection configuration",
                    "expectedName": "Subscription URL",
                    "expectedControlType": "Edit",
                    "expectedProcessId": 42,
                    "dumpDirectory": str(controller._windows_wer_dump_dir),
                },
            )
            self.assertEqual(result["xaml_content_root_peers"], root_peer_result)
            self.assertEqual(result["external_uia_point"]["completed"], True)
            self.assertEqual(result["external_uia_point"]["targetMetadataCompleted"], True)
            retained = json.loads(
                (controller.logs / "windows-content-root-diagnostics.json").read_text(encoding="utf-8")
            )
            self.assertEqual(retained["post_probe_process"], {"alive": True, "pid": 42})

    def test_windows_no_uia_hold_uses_only_win32_process_probes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            controller = object.__new__(smoke.NativeUIController)
            controller.logs = Path(directory)
            controller._call = mock.Mock(side_effect=(
                {"ready": True, "pid": 42, "windowHandle": "0x100"},
                {"alive": True, "pid": 42, "identity": "candidate-ui-instance"},
            ))

            result = controller._run_windows_no_uia_hold(0)

            self.assertEqual(
                [call.args[0] for call in controller._call.call_args_list],
                ["windows-baseline", "probe"],
            )
            self.assertEqual(result["automation_queries"], 0)
            self.assertEqual(result["complete"], True)
            self.assertEqual(len(result["process_probes"]), 1)
            retained = json.loads(
                (Path(directory) / "windows-configure-tree-no-uia-diagnostics.json")
                .read_text(encoding="utf-8")
            )
            self.assertEqual(retained["complete"], True)

    def test_windows_no_uia_hold_retains_a_failed_process_probe(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            controller = object.__new__(smoke.NativeUIController)
            controller.logs = Path(directory)
            controller._call = mock.Mock(side_effect=(
                {"ready": True, "pid": 42, "windowHandle": "0x100"},
                {"alive": False, "pid": 42, "identity": "candidate-ui-instance"},
            ))

            with self.assertRaisesRegex(smoke.NativeUISmokeError, "not alive"):
                controller._run_windows_no_uia_hold(0)

            retained = json.loads(
                (Path(directory) / "windows-configure-tree-no-uia-diagnostics.json")
                .read_text(encoding="utf-8")
            )
            self.assertEqual(retained["complete"], False)
            self.assertEqual(retained["process_probes"][0]["alive"], False)

    def test_windows_wer_collection_retains_event_streams_and_dump_inventory(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dump_dir = root / "windows-wer-dumps"
            dump_dir.mkdir()
            dump = dump_dir / "DobbyVPN.exe.1234.dmp"
            dump.write_bytes(b"minidump")
            partial = dump_dir / "uia-point-failure-42-test.dmp.partial"
            partial.write_bytes(b"partial")
            controller = object.__new__(smoke.NativeUIController)
            controller.logs = root
            controller._windows_wer_started_at_utc = smoke.datetime.now(smoke.timezone.utc)
            controller._windows_wer_dump_dir = dump_dir
            event_xml = b"<Event><System><EventID>1000</EventID></System></Event>\r\n"
            event_stderr = b"PowerShell diagnostic warning\r\n"
            completed = subprocess.CompletedProcess(
                ["powershell.exe"], 0, event_xml, event_stderr
            )

            with (
                mock.patch.object(
                    smoke, "_windows_job_capture_callbacks",
                    return_value=(mock.Mock(), mock.Mock(), mock.Mock()),
                ),
                mock.patch.object(smoke, "_native_run", return_value=completed) as run,
            ):
                controller._collect_windows_crash_diagnostics()
                controller._write_windows_wer_dump_inventory()

            self.assertIn("LogName = 'Application'", run.call_args.args[0][-1])
            self.assertIn("Id = @(1000, 1001)", run.call_args.args[0][-1])
            self.assertEqual(
                (root / "windows-wer-application-events.stdout.log").read_bytes(),
                event_xml,
            )
            self.assertEqual(
                (root / "windows-wer-application-events.stderr.log").read_bytes(),
                event_stderr,
            )
            inventory = (root / "windows-wer-dump-inventory.log").read_text(encoding="utf-8")
            self.assertIn(str(dump), inventory)
            self.assertIn("bytes=8", inventory)
            self.assertIn(f"wer_dump_partial_file={partial}", inventory)
            self.assertIn("bytes=7", inventory)

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

    def test_native_ui_helper_checks_narrow_render_and_clipboard_availability(self) -> None:
        source = WINDOWS_NATIVE_UI.read_text(encoding="utf-8")

        for assertion in (
            'VerifyNarrowWindow(root, window, process.Id, Text("source"));',
            'WaitForPasteAvailability(false, "empty");',
            'WaitForPasteAvailability(false, "non-text");',
            'WaitForPasteAvailability(true, "text");',
            'pasteInvokedAtUnixMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();',
            '["paste_invoked_at_unix_ms"] = pasteInvokedAtUnixMs,',
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
            'TracePhase("tree-uia-targeted-start")',
            'var visibleControls = Walk(root, message => TracePhase($"tree-uia-{message}"))',
            'var profileCount = visibleControls.Count(element =>',
            'if (profileCount >= 8192)',
            'var pasteButton = Walk(root).FirstOrDefault(element =>',
            'AutomationElement? FindBy(bool automationId)',
            'TracePhase($"tree-uia-targeted-complete controls={visibleControls.Count} profiles={profileCount}");',
            'catch (COMException error) when (error.HResult == unchecked((int)0x8000FFFF))',
            'uiaError = error.ToString()',
            '((WindowPattern)windowPattern).Current.CanMaximize',
            '"Could not restore native window bounds after narrow-window test"',
            'originalBounds.Right - originalBounds.Left',
            'originalBounds.Bottom - originalBounds.Top',
        ):
            with self.subTest(assertion=assertion):
                self.assertIn(assertion, source)
        self.assertNotIn("root.FindFirst(TreeScope.Subtree", source)
        tree_operation = source.split('if (operation == "tree")', 1)[1].split(
            "long? pasteInvokedAtUnixMs", 1
        )[0]
        self.assertIn("Walk(root", tree_operation)
        self.assertIn("children-start node={count} depth={depth} path={path}", source)
        self.assertNotIn("FindAll(", tree_operation)
        window_source = (
            PRODUCT_ROOT / "ui/windows/DobbyVPN.Windows/MainWindow.xaml.cs"
        ).read_text(encoding="utf-8")
        self.assertIn(
            'AutomationProperties.SetAutomationId(description, $"Profile {profile.Index + 1} description");',
            window_source,
        )
        window_xaml = (
            PRODUCT_ROOT / "ui/windows/DobbyVPN.Windows/MainWindow.xaml"
        ).read_text(encoding="utf-8")
        for automation_id in ("About", "Paste", "Retry", "Active connection action", "Clear", "Save logs"):
            with self.subTest(automation_id=automation_id):
                self.assertIn(f'AutomationProperties.AutomationId="{automation_id}"', window_xaml)
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

    def test_native_ui_profile_list_layout_uses_viewport_scroll_and_clipped_actions(self) -> None:
        source = WINDOWS_NATIVE_UI.read_text(encoding="utf-8")
        layout_start = source.index(
            "private static Dictionary<string, object?> ProfileListLayout("
        )
        scroll_start = source.index(
            "private static Dictionary<string, object?> ScrollProfileList(", layout_start
        )
        scroll_end = source.index("private static void TracePhase(", scroll_start)
        layout = source[layout_start:scroll_start]
        scroll = source[scroll_start:scroll_end]

        self.assertIn('operation == "profile-list-layout"', source)
        self.assertIn('operation == "scroll-profile-list"', source)
        self.assertIn("FindAll(", layout)
        self.assertNotIn("Walk(", layout)
        self.assertIn('var profileViewport = RequireAutomationId(root, "Profile list viewport")', layout)
        self.assertIn("FullyInside(actionBounds, viewportBounds)", layout)
        self.assertIn('"visible_profile_actions"', layout)
        self.assertIn('"scroll_position"', layout)
        self.assertIn('"profile_rows"', layout)
        self.assertIn('"name"', layout)
        self.assertIn('"protocol"', layout)
        self.assertIn('"action"', layout)
        self.assertIn('RequireAutomationId(root, "Profile list viewport")', scroll)
        self.assertIn("viewport.TryGetCurrentPattern(ScrollPattern.Pattern", scroll)
        self.assertIn("double.TryParse(position, NumberStyles.Float, CultureInfo.InvariantCulture", scroll)
        self.assertIn("double.IsFinite(targetPosition)", scroll)
        self.assertIn("targetPosition < 0 || targetPosition > 100", scroll)
        self.assertIn("SetScrollPercent(ScrollPattern.NoScroll, targetPosition)", scroll)
        self.assertIn("Math.Abs(actualPosition - targetPosition) <= 1", scroll)

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

    def test_windows_window_helpers_allow_bounded_window_discovery(self) -> None:
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
                for operation in (
                    "tree",
                    "windows-baseline",
                ):
                    controller._call(operation)

        self.assertEqual(
            [call.kwargs["timeout_seconds"] for call in run.call_args_list],
            [30.0, 30.0],
        )
        self.assertEqual(
            [json.loads(call.kwargs["input_bytes"])["operation"] for call in run.call_args_list],
            ["tree", "windows-baseline"],
        )

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

    def test_native_case_driver_runs_windows_configure_tree_diagnostics(self) -> None:
        class FakeController:
            def __init__(self, *_args, **_kwargs):
                self.operations: list[str] = []
                self.windows_content_root_diagnostics = None
                self.windows_no_uia_diagnostics = None

            @staticmethod
            def bounded_by(_timeout):
                return mock.MagicMock(__enter__=mock.Mock(), __exit__=mock.Mock(return_value=False))

            def enable_windows_crash_diagnostics(self):
                self.operations.append("enable-wer")

            def start(
                self,
                *,
                windows_content_root_diagnostics=False,
                windows_no_uia_hold_seconds=None,
            ):
                if windows_no_uia_hold_seconds is not None:
                    self.operations.append(
                        f"start-no-uia-hold={windows_no_uia_hold_seconds}"
                    )
                    self.windows_no_uia_diagnostics = {
                        "complete": True,
                        "automation_queries": 0,
                    }
                    return self.windows_no_uia_diagnostics
                self.operations.append(
                    f"start-content-root-diagnostics={windows_content_root_diagnostics}"
                )
                if windows_content_root_diagnostics:
                    self.windows_content_root_diagnostics = {
                        "xaml_content_root_peers": {
                            **_windows_content_root_probe(),
                        },
                        "external_uia_point": {
                            "schema": "dobbyvpn.windows-uia-point/v1",
                            "completed": True,
                            "ready": True,
                            "fromPointCompleted": True,
                            "targetFound": True,
                            "matchesExpected": {"all": True},
                            "processAliveAfterQuery": True,
                        },
                        "post_probe_process": {"alive": True},
                    }
                    return self.windows_content_root_diagnostics
                return {"status": "Disconnected", "labels": ["Connection configuration"]}

            def configure(self):
                self.operations.append("rendered-configure")
                return {"input_verified": True, "labels": ["Profile 1 action"]}

            def close_for_cleanup(self):
                self.operations.append("close")

            def collect_diagnostics(self):
                self.operations.append("collect")

            def restore_windows_crash_diagnostics(self):
                self.operations.append("restore-wer")

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

            def run_case(
                selected_case=WINDOWS_CONFIGURE_TREE_CASE,
            ) -> tuple[dict, FakeController]:
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
                        platform="windows",
                        native_cases=[selected_case],
                        raw_log_dir=root / "logs",
                        ui=Path("app"),
                        profile=profile,
                        timeout=30,
                        ui_helper=Path("helper"),
                    ))
                return result, constructed[0]

            windows, windows_controller = run_case()
            no_uia, no_uia_controller = run_case(WINDOWS_CONFIGURE_TREE_NO_UIA_CASE)
        self.assertEqual(windows["coverage"]["native_cases"], [WINDOWS_CONFIGURE_TREE_CASE])
        self.assertEqual(
            windows["checks"][WINDOWS_CONFIGURE_TREE_CASE]["configure_tree"]
            ["xaml_content_root_peers"]["completed"], True,
        )
        self.assertEqual(
            windows["checks"][WINDOWS_CONFIGURE_TREE_CASE]["configure_tree"]
            ["rendered_controls_queried"],
            True,
        )
        self.assertEqual(windows_controller.operations, [
            "enable-wer",
            "start-content-root-diagnostics=True",
            "close", "collect", "restore-wer",
        ])
        self.assertEqual(
            no_uia["coverage"]["native_cases"],
            [WINDOWS_CONFIGURE_TREE_NO_UIA_CASE],
        )
        self.assertEqual(
            no_uia["checks"][WINDOWS_CONFIGURE_TREE_NO_UIA_CASE]
            ["windows_no_uia_hold"]["automation_queries"],
            0,
        )
        self.assertEqual(no_uia_controller.operations, [
            "enable-wer", "start-no-uia-hold=20", "close", "collect", "restore-wer",
        ])

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
