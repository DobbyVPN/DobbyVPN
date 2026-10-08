from __future__ import annotations

import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from contextlib import nullcontext
from unittest import mock

from PIL import Image


PRODUCT_ROOT = Path(__file__).resolve().parents[4]
TORTURER_ROOT = PRODUCT_ROOT / "torturer"
WINDOWS_APP = PRODUCT_ROOT / "ui/windows/DobbyVPN.Windows/App.xaml.cs"
if str(TORTURER_ROOT) not in sys.path:
    sys.path.insert(0, str(TORTURER_ROOT))
from torturer_runner.ui import journey, smoke  # noqa: E402


def _rendered_row(marker: str, severity: str, foreground: int | None) -> dict[str, object]:
    return {
        "text": (
            f"2026-10-08T00:00:00+00:00 · {severity} · Backend · "
            f"windows-native-ui-test {marker}\nfixture {severity}"
        ),
        "foreground": foreground,
        "offscreen": False,
        "bounds": {"x": 10, "y": 20, "width": 150, "height": 30},
        "visible_in_viewport": True,
    }


class WindowsL3PaletteTests(unittest.TestCase):
    def test_app_theme_override_is_startup_only_and_leaves_unset_default_untouched(self) -> None:
        source = WINDOWS_APP.read_text(encoding="utf-8")
        initialize = source.index("InitializeComponent();")
        override = source.index('Environment.GetEnvironmentVariable("DOBBYVPN_TEST_REQUESTED_THEME")')
        launched = source.index("protected override void OnLaunched")

        self.assertLess(initialize, override)
        self.assertLess(override, launched)
        self.assertIn("if (testTheme is not null)", source)
        self.assertIn('"Light" => ApplicationTheme.Light', source)
        self.assertIn('"Dark" => ApplicationTheme.Dark', source)

    def test_theme_override_is_per_child_and_ordinary_start_clears_inherited_value(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            processes = [mock.Mock(pid=41), mock.Mock(pid=42)]
            for process in processes:
                process.poll.return_value = None
            launch_environments: list[dict[str, str]] = []

            def popen(_command, **kwargs):
                launch_environments.append(kwargs["env"])
                return processes[len(launch_environments) - 1]

            with mock.patch.dict(os.environ, {smoke._WINDOWS_TEST_THEME_VARIABLE: "Dark"}, clear=False), \
                 mock.patch.object(smoke.subprocess, "Popen", side_effect=popen):
                for index, theme in enumerate(("Light", None)):
                    app_root = root / str(index)
                    app_root.mkdir()
                    helper = app_root / "NativeUI.exe"
                    helper.touch()
                    controller = smoke.NativeUIController(
                        "windows",
                        app_root / "DobbyVPN.exe",
                        app_root / "profile.txt",
                        5,
                        helper=helper,
                        screenshot_dir=app_root / "screenshots",
                    )
                    controller._alive = mock.Mock(return_value=False)
                    controller._wait = lambda predicate, message: self.assertTrue(predicate(), message)

                    def call(operation: str, **_fields: object) -> dict[str, object]:
                        if operation == "probe":
                            controller.identity = f"instance-{index}"
                            return {"alive": True, "identity": controller.identity}
                        return {}

                    controller._call = call
                    controller.snapshot = mock.Mock(return_value={
                        "status": "Disconnected",
                        "labels": ["Connection configuration"],
                    })
                    controller.capture = mock.Mock(return_value={
                        "path": str(app_root / "startup.png"), "width": 1, "height": 1,
                    })
                    controller.start(windows_requested_theme=theme)
                self.assertEqual(
                    launch_environments[0][smoke._WINDOWS_TEST_THEME_VARIABLE], "Light"
                )
                self.assertNotIn(smoke._WINDOWS_TEST_THEME_VARIABLE, launch_environments[1])
                self.assertEqual(os.environ[smoke._WINDOWS_TEST_THEME_VARIABLE], "Dark")

    def test_theme_override_rejects_warm_or_non_windows_launch(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            helper = root / "NativeUI.exe"
            helper.touch()
            controller = smoke.NativeUIController(
                "windows", root / "DobbyVPN.exe", root / "profile.txt", 5,
                helper=helper, screenshot_dir=root / "screenshots",
            )
            with self.assertRaisesRegex(ValueError, "cold Windows launch"):
                controller.start(import_url="https://example.invalid/subscription", windows_requested_theme="Dark")
            with self.assertRaisesRegex(ValueError, "cold Windows launch"):
                controller.start(windows_requested_theme="system")

    def test_rendered_palette_measures_requested_visible_row_from_real_log_view(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            helper = root / "NativeUI.exe"
            helper.touch()
            controller = smoke.NativeUIController(
                "windows", root / "DobbyVPN.exe", root / "profile.txt", 5,
                helper=helper, screenshot_dir=root / "screenshots",
            )
            marker = "windows-l3-light-test"
            rows = [_rendered_row(marker, "INFO", 0x00FFFFFF)]
            calls: list[str] = []

            def call(operation: str, **_fields: object) -> dict[str, object]:
                calls.append(operation)
                if operation == "logs":
                    return {
                        "entries": rows,
                        "logs_viewport": {"x": 0, "y": 0, "width": 500, "height": 500},
                    }
                return {"ready": True}

            controller._call = call
            controller._wait = lambda predicate, message: self.assertTrue(predicate(), message)
            result = controller.verify_windows_palette_fixture(marker, "INFO")

            self.assertEqual(calls, ["scroll-logs", "logs"])
            self.assertEqual(result["palette"], {"INFO": 0x00FFFFFF})
            self.assertEqual(result["marker"], marker)
            with self.assertRaises(ValueError):
                controller.verify_windows_palette_fixture("", "INFO")

            rows[0] = _rendered_row(marker, "INFO", None)
            self.assertEqual(
                controller.verify_windows_palette_fixture(marker, "INFO")["palette"],
                {"INFO": None},
            )

            rows[0] = _rendered_row(marker, "WARN", 0x0000AAFF)
            self.assertEqual(
                controller.verify_windows_palette_fixture(marker, "WARN")["palette"],
                {"WARN": 0x0000AAFF},
            )

            rows[0] = _rendered_row(marker, "INFO", 0x00FFFFFF)
            rows[0]["offscreen"] = True
            rows[0]["visible_in_viewport"] = False
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "not visible in the log viewport"):
                controller.verify_windows_palette_fixture(marker, "INFO")

    def test_row_pixel_measurement_distinguishes_alpha_only_muted_and_normal_text(self) -> None:
        def capture(name: str, alpha: int) -> dict[str, object]:
            path = root / f"{name}.png"
            background = (255, 255, 255)
            foreground = (0, 0, 0)
            composited = tuple(
                round((alpha * color + (255 - alpha) * base) / 255)
                for color, base in zip(foreground, background)
            )
            image = Image.new("RGB", (24, 24), background)
            for x, y in ((7, 8), (8, 8), (9, 8), (7, 9), (8, 9)):
                image.putpixel((x, y), composited)
            image.save(path, format="PNG")
            return {
                "path": str(path), "width": 24, "height": 24,
                "screen_bounds": {"x": 100, "y": 200, "width": 24, "height": 24},
            }

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            row = {"x": 105, "y": 205, "width": 10, "height": 10}
            muted = smoke._measure_windows_palette_pixels(capture("muted", 0x9E), row)
            normal = smoke._measure_windows_palette_pixels(capture("normal", 0xE4), row)

        self.assertNotEqual(muted["foreground_rgb"], normal["foreground_rgb"])
        self.assertEqual(muted["background_rgb"], [255, 255, 255])
        self.assertLess(muted["distance_squared"], normal["distance_squared"])
        self.assertLess(muted["contrast_ratio"], normal["contrast_ratio"])
        self.assertGreaterEqual(muted["foreground_pixels"], 3)

    def test_windows_capture_forwards_physical_screen_bounds_with_original_png(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            helper = root / "NativeUI.exe"
            helper.touch()
            controller = smoke.NativeUIController(
                "windows", root / "DobbyVPN.exe", root / "profile.txt", 5,
                helper=helper, screenshot_dir=root / "screenshots",
            )
            controller._alive = mock.Mock(return_value=True)

            def capture(operation: str, **fields: object) -> dict[str, object]:
                self.assertEqual(operation, "capture")
                path = Path(str(fields["path"]))
                image = Image.new("RGB", (4, 3), (255, 255, 255))
                image.putpixel((0, 0), (0, 0, 0))
                image.save(path, format="PNG")
                return {
                    "ready": True,
                    "screen_bounds": {"x": 120, "y": 80, "width": 4, "height": 3},
                }

            controller._call = capture
            screenshot = controller.capture("palette-row")

        self.assertEqual(screenshot["width"], 4)
        self.assertEqual(screenshot["height"], 3)
        self.assertEqual(
            screenshot["screen_bounds"],
            {"x": 120, "y": 80, "width": 4, "height": 3},
        )

    def test_row_pixel_measurement_rejects_uniform_crop_and_invalid_geometry(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "row.png"
            image = Image.new("RGB", (24, 24), (255, 255, 255))
            image.putpixel((1, 1), (0, 0, 0))  # Keep the whole PNG nonuniform outside the row.
            image.save(path, format="PNG")
            screenshot = {
                "path": str(path), "width": 24, "height": 24,
                "screen_bounds": {"x": 100, "y": 200, "width": 24, "height": 24},
            }

            with self.assertRaisesRegex(smoke.NativeUISmokeError, "no visible foreground"):
                smoke._measure_windows_palette_pixels(
                    screenshot, {"x": 105, "y": 205, "width": 10, "height": 10}
                )
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "outside the captured window"):
                smoke._measure_windows_palette_pixels(
                    screenshot, {"x": 123, "y": 205, "width": 2, "height": 10}
                )
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "dimensions do not match"):
                smoke._measure_windows_palette_pixels(
                    {**screenshot, "screen_bounds": {"x": 100, "y": 200, "width": 23, "height": 24}},
                    {"x": 105, "y": 205, "width": 10, "height": 10},
                )

    def test_palette_fixture_appends_valid_tagged_rows_without_rewriting_existing_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "service.log"
            original = b"existing log bytes, including non-UTF8: \xff\n"
            path.write_bytes(original)

            with self.assertRaises(journey.NativeUIJourneyError):
                journey._append_windows_palette_fixture(path, "bad-severity", "TRACE", 1)

            results = [
                journey._append_windows_palette_fixture(
                    path, "windows-l3-fixture-test", severity, sequence
                )
                for sequence, severity in enumerate(smoke._WINDOWS_PALETTE_SEVERITIES, start=1)
            ]
            data = path.read_bytes()
            appended = data[len(original):].splitlines()
            rows = [json.loads(line) for line in appended if line]

        self.assertTrue(data.startswith(original))
        self.assertTrue(all(result["synthetic"] for result in results))
        self.assertEqual(
            [result["severity"] for result in results],
            ["DEBUG", "INFO", "WARN", "ERROR"],
        )
        self.assertEqual([row["level"] for row in rows], ["DEBUG", "INFO", "WARN", "ERROR"])
        self.assertEqual([row["process_sequence"] for row in rows], [1, 2, 3, 4])
        self.assertTrue(all(row["run_id"] == "windows-l3-fixture-test" for row in rows))
        self.assertTrue(all(row["event"] == "test.rendered-palette" for row in rows))

    def test_palette_frontends_restart_normally_and_keep_theme_evidence(self) -> None:
        class FakeController:
            def __init__(self, logs: Path):
                self.logs = logs
                self.events: list[tuple[str, object]] = []
                self.theme: str | None = None
                self.last_severity: str | None = None

            @staticmethod
            def bounded_by(_timeout):
                return nullcontext()

            def close(self):
                self.events.append(("close", self.theme))
                return {"closed": True}

            def start(self, *, windows_requested_theme=None):
                self.theme = windows_requested_theme
                self.events.append(("start", windows_requested_theme))
                return {"status": "Disconnected"}

            def verify_windows_palette_fixture(self, marker, severity):
                self.events.append(("verify", severity))
                self.assert_fixture(marker, severity)
                self.last_severity = severity
                offset = 0 if self.theme == "Light" else 1
                palette = {
                    "DEBUG": 0x00FFFFFF if offset == 0 else 0x00111111,
                    "INFO": 0x00FFFFFF if offset == 0 else 0x00111111,
                    "WARN": 0x0000AAFF,
                    "ERROR": 0x000000FF,
                }
                return {
                    "marker": marker,
                    "palette": {severity: palette[severity]},
                    "rows": {severity: {"foreground": palette[severity], "uia_foreground": palette[severity], "bounds": {"x": 1, "y": 2, "width": 3, "height": 4}}},
                    "logs_viewport": {"x": 0, "y": 0, "width": 500, "height": 500},
                }

            def assert_fixture(self, marker, severity):
                rows = [
                    json.loads(line)
                    for line in (self.logs / "service.log").read_text().splitlines()
                    if line.startswith("{")
                ]
                matches = [
                    row for row in rows
                    if row.get("run_id") == marker and row.get("level") == severity
                ]
                if len(matches) != 1:
                    raise AssertionError(
                        f"fixture marker {marker} severity {severity} was not appended exactly once"
                    )
                if matches[0].get("process_sequence") != len(self.rows_for_run()):
                    raise AssertionError("fixture sequence did not increase with prior synthetic rows")

            def rows_for_run(self):
                return [
                    json.loads(line)
                    for line in (self.logs / "service.log").read_text().splitlines()
                    if line.startswith("{") and json.loads(line).get("event") == "test.rendered-palette"
                ]

            def capture(self, milestone):
                self.events.append(("capture", milestone))
                path = self.logs / f"{milestone}-{len(self.events)}.png"
                bg = (255, 255, 255) if self.theme == "Light" else (17, 17, 17)
                palette = {
                    "Light": {"DEBUG": (96, 96, 96), "INFO": (24, 24, 24)},
                    "Dark": {"DEBUG": (125, 125, 125), "INFO": (245, 245, 245)},
                }
                fg = (
                    palette[self.theme][self.last_severity]
                    if self.last_severity in {"DEBUG", "INFO"}
                    else (157, 93, 0) if self.last_severity == "WARN" else (196, 43, 28)
                )
                image = Image.new("RGB", (20, 20), bg)
                for point in ((1, 2), (2, 2), (3, 2)):
                    image.putpixel(point, fg)
                image.save(path, format="PNG")
                return {
                    "path": str(path), "width": 20, "height": 20,
                    "screen_bounds": {"x": 0, "y": 0, "width": 20, "height": 20},
                }

            def close_for_cleanup(self):
                self.events.append(("close-for-cleanup", self.theme))

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            log_dir = root / "logs"
            log_dir.mkdir()
            log_path = log_dir / "service.log"
            log_path.write_bytes(b"initial\n")
            ui = FakeController(log_dir)
            checks: dict[str, object] = {}

            journey._exercise_windows_rendered_log_palette(ui, log_path, 30, checks)

        self.assertTrue(checks["windows_rendered_log_palette"])
        evidence = checks["windows_rendered_log_palette_evidence"]
        self.assertEqual(set(evidence), {"Light", "Dark", "normal_frontend"})
        self.assertEqual(evidence["Light"]["palette"]["INFO"], [24, 24, 24])
        self.assertEqual(evidence["Dark"]["palette"]["INFO"], [245, 245, 245])
        self.assertEqual(evidence["Light"]["uia_palette"]["INFO"], 0x00FFFFFF)
        self.assertEqual(evidence["Dark"]["uia_palette"]["INFO"], 0x00111111)
        self.assertEqual(
            evidence["Light"]["uia_palette"]["DEBUG"],
            evidence["Light"]["uia_palette"]["INFO"],
        )
        self.assertLess(
            evidence["Light"]["rows"]["DEBUG"]["pixel_measurement"]["distance_squared"],
            evidence["Light"]["rows"]["INFO"]["pixel_measurement"]["distance_squared"],
        )
        self.assertEqual(evidence["Light"]["rows"]["DEBUG"]["bounds"]["x"], 1)
        self.assertEqual(evidence["Dark"]["logs_viewport"]["INFO"]["width"], 500)
        self.assertEqual(
            [value for event, value in ui.events if event == "verify"],
            ["DEBUG", "INFO", "WARN", "ERROR"] * 2,
        )
        self.assertEqual(
            len([value for event, value in ui.events if event == "capture"]),
            8,
        )
        self.assertEqual(ui.events[-1], ("start", None))

    def test_palette_failure_keeps_primary_and_normal_frontend_restore_errors(self) -> None:
        class FailingController:
            def __init__(self, logs: Path):
                self.logs = logs
                self.events: list[str] = []
                self.theme = None

            @staticmethod
            def bounded_by(_timeout):
                return nullcontext()

            def close(self):
                self.events.append("close")
                return {"closed": True}

            def start(self, *, windows_requested_theme=None):
                self.theme = windows_requested_theme
                self.events.append(f"start:{windows_requested_theme}")
                if windows_requested_theme is None:
                    raise RuntimeError("normal restart failed")
                return {"status": "Disconnected"}

            def verify_windows_palette_fixture(self, _marker, _severity):
                raise RuntimeError("foreground assertion failed")

            def capture(self, _milestone):
                return {"path": str(self.logs / "failure.png"), "width": 1, "height": 1}

            def close_for_cleanup(self):
                self.events.append("close-for-cleanup")
                raise RuntimeError("palette frontend close failed")

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            log_dir = root / "logs"
            log_dir.mkdir()
            log_path = log_dir / "service.log"
            log_path.write_bytes(b"initial\n")
            ui = FailingController(log_dir)
            checks: dict[str, object] = {}

            with self.assertRaisesRegex(journey.NativeUIJourneyError, "foreground assertion failed") as raised:
                journey._exercise_windows_rendered_log_palette(ui, log_path, 30, checks)

        notes = "\n".join(raised.exception.__notes__)
        self.assertIn("Windows palette restore", notes)
        self.assertIn("normal restart failed", notes)
        self.assertIn("palette frontend close failed", notes)
        self.assertIn("close-for-cleanup", ui.events)
        self.assertEqual(ui.events[-1], "start:None")


if __name__ == "__main__":
    unittest.main()
