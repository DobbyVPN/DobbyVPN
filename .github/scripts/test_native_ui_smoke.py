from __future__ import annotations

import io
import json
import base64
import plistlib
import signal
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, call, patch

import native_ui_smoke as smoke
from torturer_checks.hosted import native_ui as hosted_native_ui


class NativeUISmokeIdentityTests(unittest.TestCase):
    _MACOS_EXECUTABLE = "/tmp/Dobby UI/Dobby Vpn.app/Contents/MacOS/Dobby Vpn"

    def test_native_run_emits_complete_success_streams_with_boundaries_and_redaction(self) -> None:
        result = subprocess.CompletedProcess(
            ["probe"], 0, stdout="prefix-short-secret-suffix", stderr="diagnostic",
        )
        captured = io.StringIO()
        with patch.object(smoke.subprocess, "run", return_value=result), \
                patch.object(smoke.sys, "stderr", captured):
            smoke._native_run(
                ["probe"],
                check=False,
                capture_output=True,
                stream_label="success-probe",
                sensitive_values=("short-secret",),
            )
        output = captured.getvalue()
        self.assertIn("[native subprocess success-probe stdout begin]", output)
        self.assertIn("prefix-[REDACTED]-suffix\n", output)
        self.assertIn("[native subprocess success-probe stdout end]", output)
        self.assertIn("diagnostic\n", output)
        self.assertNotIn("short-secret", output)

    def test_native_run_emits_timeout_streams_before_reraising(self) -> None:
        failure = subprocess.TimeoutExpired(
            ["probe"], 1, output=b"partial-out", stderr=b"partial-err",
        )
        captured = io.StringIO()
        with patch.object(smoke.subprocess, "run", side_effect=failure), \
                patch.object(smoke.sys, "stderr", captured):
            with self.assertRaises(subprocess.TimeoutExpired):
                smoke._native_run(["probe"], check=False, capture_output=True)
        output = captured.getvalue()
        self.assertIn("partial-out", output)
        self.assertIn("partial-err", output)
        self.assertIn("[native subprocess probe stdout end]", output)

    def test_windows_identity_reports_the_attached_console_session(self) -> None:
        result = subprocess.CompletedProcess(
            ["powershell"], 0, stdout="CONTOSO\\runner|session=2|userInteractive=True\n", stderr="",
        )
        with patch.object(smoke.shutil, "which", return_value="powershell"), \
                patch.object(smoke.subprocess, "run", return_value=result):
            self.assertEqual(
                smoke._windows_interactive_identity(),
                "CONTOSO\\runner|session=2|userInteractive=True",
            )

    def test_windows_service_session_is_unavailable_not_a_pass(self) -> None:
        result = subprocess.CompletedProcess(
            ["powershell"], 1, stdout="", stderr="GUI process is not in an interactive console session",
        )
        with patch.object(smoke.shutil, "which", return_value="powershell"), \
                patch.object(smoke.subprocess, "run", return_value=result):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "interactive console"):
                smoke._windows_interactive_identity()

    def test_macos_requires_process_identity_to_match_console_user(self) -> None:
        console = subprocess.CompletedProcess(
            ["scutil"],
            0,
            stdout=b"kCGSSessionUserNameKey : alice\n"
            b"kCGSSessionUserIDKey : 501\n",
            stderr=b"",
        )
        with patch.object(smoke.getpass, "getuser", return_value="runner"), \
                patch.object(smoke.subprocess, "run", return_value=console):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "does not own the console"):
                smoke._macos_interactive_identity()

    def test_macos_identity_uses_authoritative_console_state_and_accessibility(self) -> None:
        calls: list[list[str]] = []

        def run(command, **_kwargs):
            calls.append(command)
            if command[0] == "scutil":
                return subprocess.CompletedProcess(
                    command,
                    0,
                    stdout=b"kCGSSessionUserNameKey : alice\n"
                    b"kCGSSessionUserIDKey : 501\n",
                    stderr=b"",
                )
            if command[:2] == ["launchctl", "print"]:
                return subprocess.CompletedProcess(command, 0, stdout="gui\n", stderr="")
            if command[0] == "osascript":
                return subprocess.CompletedProcess(command, 0, stdout="Finder\n", stderr="")
            raise AssertionError(command)

        with (
            patch.object(smoke.getpass, "getuser", return_value="alice"),
            patch.object(smoke.os, "getuid", return_value=501),
            patch.object(smoke.subprocess, "run", side_effect=run) as run_mock,
        ):
            self.assertEqual(
                smoke._macos_interactive_identity(),
                "alice|uid=501|console=alice",
            )
        self.assertEqual(run_mock.call_args_list[0].kwargs["input"], b"show State:/Users/ConsoleUser\nquit\n")
        self.assertEqual(calls[0], ["scutil"])
        self.assertNotIn("stat", [command[0] for command in calls])

    def test_macos_accessibility_lookup_is_bound_to_pid_not_process_name(self) -> None:
        result = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"control","bounds":[10,20,110,220]}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            self.assertEqual(
                smoke._macos_accessibility_rect(4321, smoke._NATIVE_ACTION_LABEL, 2),
                (10, 20, 110, 220),
            )
        command = run.call_args.args[0]
        self.assertEqual(command[command.index("--pid") + 1], "4321")
        self.assertEqual(command[command.index("--name") + 1], smoke._NATIVE_ACTION_LABEL)
        self.assertNotIn("osascript", command)

    def test_macos_obstruction_lookup_uses_the_exact_pid_helper(self) -> None:
        result = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout=(
                '{"ok":true,"stage":"obstruction",'
                '"target":{"owner_pid":4321,"window_id":17,"bounds":[0,0,20,20]},'
                '"obstructions":[]}\n'
            ),
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            self.assertEqual(smoke._macos_window_obstructions(4321, 2), [])
        command = run.call_args.args[0]
        self.assertEqual(command[command.index("--pid") + 1], "4321")
        self.assertIn("--obstructions", command)

    def test_png_masking_rewrites_metadata_free_nonblank_image(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = smoke.Path(directory) / "capture.png"
            rgba = bytearray(
                b"\xff\x00\x00\xff" + b"\x00\xff\x00\xff"
                + b"\x00\x00\xff\xff" + b"\xff\xff\xff\xff"
            )
            smoke._write_png(path, 2, 2, rgba)
            self.assertEqual(smoke._mask_png(path, (0, 0, 20, 20), [(0, 0, 10, 20)]), (2, 2))
            width, height, masked = smoke._read_png(path)
            self.assertEqual((width, height), (2, 2))
            self.assertEqual(tuple(masked[:4]), (128, 128, 128, 255))
            self.assertNotIn(b"tEXt", path.read_bytes())

    def test_screenshot_milestone_rejects_path_traversal(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(smoke.NativeUIScreenshotError):
                smoke._screenshot_path(smoke.Path(directory), "../private", 42)

    def test_macos_window_probe_uses_the_same_exact_pid_helper(self) -> None:
        result = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"window","bounds":[1,2,461,522]}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            self.assertEqual(smoke._macos_window_rect(4321, 2), (1, 2, 461, 522))
        command = run.call_args.args[0]
        self.assertEqual(command[command.index("--pid") + 1], "4321")
        self.assertIn("--window", command)

    def test_macos_window_title_probe_uses_the_same_exact_pid_helper(self) -> None:
        result = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"window-title","title":"Dobby VPN — Connected"}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            self.assertEqual(smoke._macos_window_title(4321, 2), "Dobby VPN — Connected")
        command = run.call_args.args[0]
        self.assertEqual(command[command.index("--pid") + 1], "4321")
        self.assertIn("--window-title", command)

    def test_macos_window_title_retries_transient_ax_title_state(self) -> None:
        transient = subprocess.CompletedProcess(
            ["macos_ax.py"],
            1,
            stdout='{"ok":false,"stage":"ax-window-title","transient":true,"error":"status=-25204"}\n',
            stderr="",
        )
        ready = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"window-title","title":"Dobby VPN — Disconnected"}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", side_effect=[transient, ready]) as run:
            self.assertEqual(smoke._macos_window_title(4321, 2), "Dobby VPN — Disconnected")
        self.assertEqual(run.call_count, 2)

    def test_macos_title_state_requires_exact_product_title(self) -> None:
        self.assertTrue(smoke._macos_title_has_state("Dobby VPN — Connected", "Connected"))
        self.assertFalse(smoke._macos_title_has_state("Other window — Connected", "Connected"))
        self.assertFalse(smoke._macos_title_has_state("Dobby VPN — Connected (debug)", "Connected"))
        self.assertTrue(smoke._macos_title_is_status("Dobby VPN — Disconnected"))
        self.assertFalse(smoke._macos_title_is_status("Dobby VPN"))

    def test_macos_ax_helper_keeps_child_deadline_inside_parent_timeout(self) -> None:
        result = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"window","bounds":[1,2,461,522]}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            smoke._macos_ax_request(4321, 10, window=True)
        command = run.call_args.args[0]
        self.assertEqual(float(command[command.index("--deadline") + 1]), 4.0)
        self.assertEqual(run.call_args.kwargs["timeout"], 6.0)

        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            smoke._macos_ax_request(4321, 0.8, window=True)
        command = run.call_args.args[0]
        self.assertAlmostEqual(float(command[command.index("--deadline") + 1]), 0.55)
        self.assertEqual(run.call_args.kwargs["timeout"], 0.8)

    def test_macos_accessibility_rect_retries_only_transient_window_server_state(self) -> None:
        transient = subprocess.CompletedProcess(
            ["macos_ax.py"],
            1,
            stdout='{"ok":false,"stage":"ax-windows","transient":true,"error":"cannot complete"}\n',
            stderr="",
        )
        ready = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"control","bounds":[10,20,110,220]}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", side_effect=[transient, ready]) as run:
            self.assertEqual(smoke._macos_accessibility_rect(4321, smoke._NATIVE_ACTION_LABEL, 2), (10, 20, 110, 220))
        self.assertEqual(run.call_count, 2)

    def test_macos_window_rect_retries_transient_window_server_state(self) -> None:
        transient = subprocess.CompletedProcess(
            ["macos_ax.py"],
            1,
            stdout='{"ok":false,"stage":"ax-windows","transient":true,"error":"cannot complete"}\n',
            stderr="",
        )
        ready = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"window","bounds":[1,2,461,522]}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", side_effect=[transient, ready]) as run:
            self.assertEqual(smoke._macos_window_rect(4321, 2), (1, 2, 461, 522))
        self.assertEqual(run.call_count, 2)

    def test_macos_window_raise_uses_public_helper_and_exact_pid(self) -> None:
        result = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"raise","ax_window_count":1}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            smoke._macos_ax_raise_window(4321, 2)
        command = run.call_args.args[0]
        self.assertEqual(command[command.index("--pid") + 1], "4321")
        self.assertIn("--raise-window", command)

    def test_macos_window_raise_retries_transient_window_server_state(self) -> None:
        transient = subprocess.CompletedProcess(
            ["macos_ax.py"],
            1,
            stdout='{"ok":false,"stage":"ax-windows","transient":true,"error":"cannot complete"}\n',
            stderr="",
        )
        ready = subprocess.CompletedProcess(
            ["macos_ax.py"], 0,
            stdout='{"ok":true,"stage":"raise","ax_window_count":1}\n',
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", side_effect=[transient, ready]) as run:
            smoke._macos_ax_raise_window(4321, 2)
        self.assertEqual(run.call_count, 2)

    def test_macos_ax_retry_preserves_last_transient_at_deadline(self) -> None:
        clock = [0.0]

        def request(*_args, **_kwargs):
            clock[0] = 1.0
            raise smoke.NativeUIWindowNotReady("macOS AX ax-windows: cannot complete")

        with patch.object(smoke.time, "monotonic", side_effect=lambda: clock[0]), \
                patch.object(smoke.time, "sleep"), \
                patch.object(smoke, "_macos_ax_request", side_effect=request) as ax_request:
            with self.assertRaisesRegex(smoke.NativeUIWindowNotReady, r"cannot complete \(attempts=1\)"):
                smoke._macos_window_rect(4321, 2)
        self.assertEqual(ax_request.call_count, 1)

    def test_macos_ax_retry_does_not_retry_hard_control_error(self) -> None:
        with patch.object(
            smoke,
            "_macos_ax_request",
            side_effect=smoke.NativeUIElementNotFound("ambiguous control"),
        ) as request:
            with self.assertRaisesRegex(smoke.NativeUIElementNotFound, "ambiguous control"):
                smoke._macos_accessibility_rect(4321, smoke._NATIVE_ACTION_LABEL, 2)
        self.assertEqual(request.call_count, 1)

    def test_macos_frontmost_query_rejects_non_pid_output(self) -> None:
        result = subprocess.CompletedProcess(
            ["osascript"], 0, stdout="Dobby Vpn\n", stderr=""
        )
        with patch.object(smoke.subprocess, "run", return_value=result):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "invalid output"):
                smoke._macos_frontmost_pid()

    def test_macos_click_posts_physical_events_at_ax_validated_center(self) -> None:
        class FakeGraphics:
            def __init__(self):
                self.created = []
                self.posted = []
                self.cursor = smoke._MacCGPoint(60.0, 80.0)

            def CGEventCreateMouseEvent(self, source, event_type, point, button):
                self.created.append((source, event_type, point, button))
                return f"event-{event_type}"

            def CGEventCreate(self, source):
                return "cursor-event"

            def CGEventGetLocation(self, event):
                return self.cursor

            def CGEventPost(self, tap, event):
                self.posted.append((tap, event))

        class FakeCore:
            def __init__(self):
                self.released = []

            def CFRelease(self, event):
                self.released.append(event)

        graphics = FakeGraphics()
        core = FakeCore()
        with (
            patch.object(smoke, "_macos_window_rect", return_value=(0, 0, 200, 200)),
            patch.object(smoke, "_macos_focus_window") as focus_window,
            patch.object(smoke, "_macos_core_graphics", return_value=(graphics, core)),
        ):
            smoke._macos_click((40, 60, 80, 100), 4321)
        focus_window.assert_called_once_with(4321)
        self.assertEqual([event[1] for event in graphics.created], [5, 5, 1, 2])
        self.assertEqual(graphics.created[2][2].x, 60.0)
        self.assertEqual(graphics.created[2][2].y, 80.0)
        self.assertEqual(
            graphics.posted,
            [(0, "event-5"), (0, "event-5"), (0, "event-1"), (0, "event-2")],
        )
        self.assertEqual(
            core.released,
            ["event-5", "event-5", "cursor-event", "event-1", "event-2"],
        )

    def test_macos_click_rejects_control_outside_exact_window(self) -> None:
        with patch.object(smoke, "_macos_window_rect", return_value=(0, 0, 100, 100)):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "outside"):
                smoke._macos_click((90, 90, 120, 120), 4321)

    def test_macos_has_element_only_treats_explicit_not_found_as_absent(self) -> None:
        with patch.object(
            smoke,
            "_macos_accessibility_rect",
            side_effect=smoke.NativeUIElementNotFound("not found"),
        ):
            self.assertFalse(smoke._macos_has_element(4321, smoke._NATIVE_ACTION_LABEL))
        with patch.object(
            smoke,
            "_macos_accessibility_rect",
            side_effect=smoke.NativeUISmokeError("AX timeout"),
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "AX timeout"):
                smoke._macos_has_element(4321, smoke._NATIVE_ACTION_LABEL)

    def test_macos_activation_diagnostic_reports_only_allowlisted_state_labels(self) -> None:
        def has_element(_pid, name, *, timeout):
            self.assertEqual(timeout, 1.5)
            return name in {"Ready", smoke._NATIVE_ACTION_LABEL}

        with patch.object(smoke, "_macos_has_element", side_effect=has_element):
            self.assertEqual(
                smoke._macos_allowlisted_state_labels(4321),
                ("Ready", smoke._NATIVE_ACTION_LABEL),
            )

    def test_macos_activation_diagnostic_does_not_replace_lookup_errors(self) -> None:
        with patch.object(
            smoke,
            "_macos_has_element",
            side_effect=smoke.NativeUISmokeError("AX timeout"),
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "AX timeout"):
                smoke._macos_allowlisted_state_labels(4321)

    def test_macos_ax_windows_no_value_is_retryable_startup_state(self) -> None:
        result = subprocess.CompletedProcess(
            ["macos_ax.py"],
            1,
            stdout=(
                '{"ok":false,"stage":"ax-windows","transient":true,'
                '"error":"no AXWindows (status=-25212, cg_window_count=1)"}\n'
            ),
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result):
            with self.assertRaisesRegex(smoke.NativeUIWindowNotReady, "cg_window_count=1"):
                smoke._macos_window_rect(4321, 2)

    def test_macos_keystroke_is_bound_to_pid(self) -> None:
        result = subprocess.CompletedProcess(["osascript"], 0, stdout="", stderr="")
        with patch.object(smoke, "_macos_focus_window") as focus_window, \
                patch.object(smoke.subprocess, "run", return_value=result) as run:
            smoke._macos_keystroke(4321, "v")
        script = run.call_args.args[0][2]
        self.assertIn("unix id is 4321", script)
        self.assertIn('keystroke "v" using command down', script)
        focus_window.assert_not_called()

    def test_macos_focus_next_sends_one_native_tab_to_pid(self) -> None:
        result = subprocess.CompletedProcess(["osascript"], 0, stdout="", stderr="")
        with patch.object(smoke, "_macos_focus_window"), \
                patch.object(smoke.subprocess, "run", return_value=result) as run:
            smoke._macos_focus_next(4321)
        script = run.call_args.args[0][2]
        self.assertIn("unix id is 4321", script)
        self.assertIn("key code 48", script)

    def test_windows_accessibility_uses_strict_pointer_sized_hwnd_conversion(self) -> None:
        result = subprocess.CompletedProcess(
            ["powershell"], 0, stdout="10,20,110,220\n", stderr=""
        )
        with (
            patch.object(smoke.shutil, "which", return_value="powershell"),
            patch.object(smoke.subprocess, "run", return_value=result) as run,
        ):
            self.assertEqual(
                smoke._windows_accessibility_rect(328514, smoke._NATIVE_ACTION_LABEL),
                (10, 20, 110, 220),
            )
        command = run.call_args.args[0]
        script = command[command.index("-Command") + 1]
        self.assertIn("$rawHwnd = [string]$env:DOBBY_UI_HWND", script)
        self.assertIn("^[1-9][0-9]*$", script)
        self.assertIn(
            "[Int64]::Parse($rawHwnd, [Globalization.CultureInfo]::InvariantCulture)",
            script,
        )
        self.assertIn("[IntPtr]::new($hwndValue)", script)
        self.assertNotIn("FromHandle([IntPtr]$env:DOBBY_UI_HWND)", script)
        self.assertEqual(run.call_args.kwargs["env"]["DOBBY_UI_HWND"], "328514")

    def test_macos_process_discovery_is_scoped_to_current_uid(self) -> None:
        result = subprocess.CompletedProcess(
            ["pgrep"], 0, stdout="4321\n", stderr=""
        )
        with (
            patch.object(smoke.os, "getuid", return_value=501),
            patch.object(smoke.subprocess, "run", return_value=result) as run,
        ):
            self.assertEqual(smoke._macos_process_pids(), (4321,))
        self.assertEqual(
            run.call_args.args[0],
            ["pgrep", "-x", "-u", "501", "Dobby Vpn"],
        )

    def test_macos_process_signal_requires_current_uid_ownership(self) -> None:
        identity = smoke._MacOSProcessIdentity(4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026")
        with (
            patch.object(smoke.os, "getuid", return_value=501),
            patch.object(smoke, "_macos_process_identity", return_value=identity),
            patch.object(smoke.os, "kill") as kill,
        ):
            smoke._terminate_macos_process(identity, signal.SIGTERM)
        kill.assert_called_once_with(4321, signal.SIGTERM)

    def test_macos_process_signal_fails_closed_for_other_uid(self) -> None:
        expected = smoke._MacOSProcessIdentity(4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026")
        replacement = smoke._MacOSProcessIdentity(4321, 502, self._MACOS_EXECUTABLE, "Mon Sep 20 12:35:01 2026")
        with (
            patch.object(smoke.os, "getuid", return_value=501),
            patch.object(smoke, "_macos_process_identity", return_value=replacement),
            patch.object(smoke.os, "kill") as kill,
        ):
            smoke._terminate_macos_process(expected, signal.SIGTERM)
        kill.assert_not_called()

    def test_macos_process_signal_rejects_same_uid_pid_reuse(self) -> None:
        expected = smoke._MacOSProcessIdentity(4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026")
        replacement = smoke._MacOSProcessIdentity(4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:35:01 2026")
        with (
            patch.object(smoke.os, "getuid", return_value=501),
            patch.object(smoke, "_macos_process_identity", return_value=replacement),
            patch.object(smoke.os, "kill") as kill,
        ):
            smoke._terminate_macos_process(expected, signal.SIGTERM)
        kill.assert_not_called()

    def test_macos_process_identity_reads_exact_name_and_start(self) -> None:
        result = subprocess.CompletedProcess(
            ["ps"],
            0,
            stdout=(
                " 4321  501 Mon Sep 20 12:34:56 2026 "
                "/tmp/Dobby UI/Dobby Vpn.app/Contents/MacOS/Dobby Vpn\n"
            ),
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            self.assertEqual(
                smoke._macos_process_identity(4321),
                smoke._MacOSProcessIdentity(
                    4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"
                ),
            )
        self.assertEqual(
            run.call_args.args[0],
            ["ps", "-ww", "-p", "4321", "-o", "pid=,uid=,lstart=,command="],
        )

    def test_macos_process_identity_keeps_arguments_out_of_exact_path(self) -> None:
        result = subprocess.CompletedProcess(
            ["ps"],
            0,
            stdout=(
                " 4321  501 Mon Sep 20 12:34:56 2026 "
                "/tmp/Dobby UI/Dobby Vpn.app/Contents/MacOS/Dobby Vpn --unexpected\n"
            ),
            stderr="",
        )
        with patch.object(smoke.subprocess, "run", return_value=result):
            identity = smoke._macos_process_identity(4321)
        self.assertIsNotNone(identity)
        self.assertNotEqual(identity.executable, self._MACOS_EXECUTABLE)

    def test_macos_pid_revalidation_rejects_reused_process_before_action(self) -> None:
        expected = smoke._MacOSProcessIdentity(
            4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"
        )
        replacement = smoke._MacOSProcessIdentity(
            4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:35:01 2026"
        )
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1
        )
        controller.macos_pid = expected.pid
        controller.macos_process_identity = expected
        controller.macos_expected_executable = expected.executable
        with (
            patch.object(smoke, "_macos_process_identity", return_value=replacement),
            patch.object(smoke, "_macos_click") as click,
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "identity changed"):
                controller._macos_pid_or_error()
            click.assert_not_called()

    def test_macos_pid_revalidation_rejects_exited_process_before_action(self) -> None:
        expected = smoke._MacOSProcessIdentity(
            4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"
        )
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1
        )
        controller.macos_pid = expected.pid
        controller.macos_process_identity = expected
        with patch.object(smoke, "_macos_process_identity", return_value=None):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "has exited"):
                controller._macos_pid_or_error()

    def test_macos_bundle_launch_uses_exact_path_and_allowlisted_environment(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            bundle = smoke.Path(temporary) / "Dobby VPN.app"
            executable = bundle / "Contents" / "MacOS" / "Dobby Vpn"
            executable.parent.mkdir(parents=True)
            executable.write_bytes(b"candidate")
            with (bundle / "Contents" / "Info.plist").open("wb") as stream:
                plistlib.dump({"CFBundleExecutable": "Dobby Vpn"}, stream)
            process = Mock(pid=4321, poll=Mock(return_value=None))
            controller = smoke.NativeUIController(
                "macos", bundle, smoke.Path("profile"), 1
            )
            with (
                patch.object(smoke, "_terminate_existing_macos_instances") as terminate,
                patch.object(smoke.subprocess, "Popen", return_value=process) as popen,
                patch.object(controller, "_wait"),
                patch.object(controller, "_macos_pid_or_error", return_value=4321),
                patch.dict(smoke.os.environ, {
                    "HOME": "/tmp/ui-home",
                    "DOBBYVPN_CONTROL_SOCKET": "/tmp/control.sock",
                    "DOBBYVPN_NATIVE_UI_LOG_DIR": "/tmp/ui-logs",
                }, clear=False),
            ):
                controller._launch_macos()
            terminate.assert_called_once_with(1, str(executable.resolve()))
            command = popen.call_args.args[0]
            self.assertIn("--env", command)
            self.assertIn("HOME=/tmp/ui-home", command)
            self.assertIn("DOBBYVPN_CONTROL_SOCKET=/tmp/control.sock", command)
            self.assertIn("--stdout", command)
            self.assertIn("/tmp/ui-logs/macos-app.stdout.log", command)
            self.assertIn("--stderr", command)
            self.assertIn("/tmp/ui-logs/macos-app.stderr.log", command)
            self.assertEqual(command[-1], str(bundle))

    def test_macos_reference_preflight_drains_large_stdout_and_stderr_concurrently(self) -> None:
        real_popen = subprocess.Popen

        def fake_popen(_command, **kwargs):
            child = (
                "import sys,time; "
                "sys.stderr.write('diagnostic-stderr-' * 100000); sys.stderr.flush(); "
                "print('{\"ready\":true,\"x\":1,\"y\":2,\"width\":20,\"height\":20}', flush=True); "
                "time.sleep(0.05); "
                "print('{\"clicked\":true}', flush=True); time.sleep(0.05)"
            )
            return real_popen(
                [smoke.sys.executable, "-c", child],
                stdout=kwargs["stdout"],
                stderr=kwargs["stderr"],
                bufsize=kwargs.get("bufsize", 0),
            )

        with (
            patch.object(smoke.sys, "platform", "darwin"),
            patch.object(smoke.subprocess, "Popen", side_effect=fake_popen),
            patch.object(smoke, "_macos_capture_rect"),
            patch.object(smoke, "_macos_window_obstructions", return_value=[]),
            patch.object(smoke, "_macos_post_reference_click"),
        ):
            smoke._macos_reference_event_preflight(timeout=3)

    def test_macos_raw_binary_is_rejected_before_launch(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("/tmp/Dobby Vpn"), smoke.Path("profile"), 1
        )
        with self.assertRaisesRegex(smoke.NativeUISmokeError, "product-shaped .app"):
            controller._launch_macos()

    def test_macos_launched_child_cleanup_uses_captured_identity(self) -> None:
        identity = smoke._MacOSProcessIdentity(
            4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"
        )
        process = Mock(pid=9001, poll=Mock(return_value=None))
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1
        )
        controller.process = process
        controller.macos_pid = identity.pid
        controller.macos_process_identity = identity
        with (
            patch.object(smoke, "_terminate_macos_process_tree") as terminate,
        ):
            controller.close_for_cleanup()
        terminate.assert_called_once_with(identity, 1)
        process.wait.assert_called_once_with(timeout=2)
        self.assertIsNone(controller.process)
        self.assertIsNone(controller.macos_process_identity)

    def test_macos_close_uses_cmd_q_and_waits_for_exact_app_identity(self) -> None:
        identity = smoke._MacOSProcessIdentity(
            4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"
        )
        process = Mock(pid=9001, poll=Mock(return_value=0), returncode=0)
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 3
        )
        controller.process = process
        controller.macos_pid = identity.pid
        controller.macos_process_identity = identity
        app_alive = True

        def is_alive(_identity):
            return app_alive

        def wait_for_close(predicate, message, _timeout=None):
            nonlocal app_alive
            self.assertEqual(message, "macos UI did not close")
            self.assertFalse(predicate())
            app_alive = False
            self.assertTrue(predicate())

        completed = subprocess.CompletedProcess(
            ["osascript", "-e", ""], 0, stdout="", stderr=""
        )
        with (
            patch.object(smoke, "_macos_identity_is_alive", side_effect=is_alive),
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(smoke.subprocess, "run", return_value=completed) as run,
            patch.object(controller, "_wait", side_effect=wait_for_close),
            patch.object(smoke, "_terminate_macos_process_tree") as terminate,
            patch.object(controller, "snapshot", return_value={"status": "Disconnected"}),
        ):
            self.assertEqual(controller.close(), {"status": "Disconnected"})
        command = run.call_args.args[0]
        self.assertEqual(command[:2], ["osascript", "-e"])
        self.assertIn('keystroke "q" using command down', command[2])
        terminate.assert_not_called()
        self.assertIsNone(controller.process)
        self.assertIsNone(controller.macos_process_identity)

    def test_macos_close_timeout_does_not_force_terminate_app(self) -> None:
        identity = smoke._MacOSProcessIdentity(
            4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"
        )
        process = Mock(pid=9001, poll=Mock(return_value=0), returncode=0)
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 3
        )
        controller.process = process
        controller.macos_pid = identity.pid
        controller.macos_process_identity = identity
        completed = subprocess.CompletedProcess(
            ["osascript", "-e", ""], 0, stdout="", stderr=""
        )
        with (
            patch.object(smoke, "_macos_identity_is_alive", return_value=True),
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(smoke.subprocess, "run", return_value=completed) as run,
            patch.object(
                controller,
                "_wait",
                side_effect=smoke.NativeUIWaitTimeout("still alive"),
            ),
            patch.object(smoke, "_terminate_macos_process_tree") as terminate,
        ):
            with self.assertRaisesRegex(smoke.NativeUIWaitTimeout, "still alive"):
                controller.close()
        self.assertIn('keystroke "q" using command down', run.call_args.args[0][2])
        terminate.assert_not_called()
        process.terminate.assert_not_called()
        process.kill.assert_not_called()
        self.assertIs(controller.process, process)
        self.assertIs(controller.macos_process_identity, identity)

    def test_macos_cleanup_escalates_verified_app_identity_to_kill(self) -> None:
        identity = smoke._MacOSProcessIdentity(
            4321, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"
        )
        with (
            patch.object(smoke, "_macos_identity_is_alive", return_value=True),
            patch.object(smoke, "_terminate_macos_process") as terminate,
            patch.object(
                smoke,
                "_wait_until",
                side_effect=[smoke.NativeUIWaitTimeout("still alive"), None],
            ),
        ):
            smoke._terminate_macos_process_tree(identity, 1)
        self.assertEqual(
            terminate.call_args_list,
            [call(identity, signal.SIGTERM), call(identity, signal.SIGKILL)],
        )

    def test_windows_does_not_use_title_only_stale_window_fallback(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        controller.process = Mock(pid=4321)
        with (
            patch.object(controller, "_windows_process_window", return_value=0),
            patch.object(smoke.ctypes, "windll", create=True) as windll,
        ):
            self.assertFalse(controller._windows_find_window())
        windll.user32.FindWindowW.assert_not_called()

    def test_windows_window_discovery_declares_pointer_sized_handles(self) -> None:
        with patch.object(smoke.ctypes, "windll", create=True) as windll:
            smoke._windows_user32()
        self.assertEqual(
            windll.user32.GetWindowThreadProcessId.argtypes[0],
            smoke.wintypes.HWND,
        )
        self.assertEqual(
            windll.user32.GetWindowRect.argtypes[0],
            smoke.wintypes.HWND,
        )

    def test_windows_window_timeout_diagnostics_are_scoped_to_exact_child(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        controller.process = Mock(pid=9468, poll=Mock(return_value=None))
        with (
            patch.object(controller, "_windows_matching_windows", return_value=[{
                "hwnd": 123,
                "is_window": True,
                "is_visible": False,
            }]),
            patch.object(smoke, "_windows_rect", return_value=(0, 0, 460, 520)),
        ):
            diagnostics = controller._windows_window_diagnostics()
        self.assertTrue(diagnostics["child_alive"])
        self.assertEqual(diagnostics["matching_window_count"], 1)
        self.assertEqual(
            diagnostics["matching_windows"],
            [{
                "hwnd": 123,
                "is_window": True,
                "is_visible": False,
                "rect": (0, 0, 460, 520),
            }],
        )
        self.assertEqual(diagnostics["window_enumeration"], "ok")

    def test_windows_window_diagnostics_report_enumeration_failure(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        controller.process = Mock(pid=9468, poll=Mock(return_value=None))
        with patch.object(
            controller,
            "_windows_matching_windows",
            side_effect=smoke.NativeUISmokeError("EnumWindows failed"),
        ):
            diagnostics = controller._windows_window_diagnostics()
        self.assertTrue(diagnostics["child_alive"])
        self.assertIsNone(diagnostics["matching_window_count"])
        self.assertEqual(diagnostics["window_enumeration"], "failed")

    def test_windows_window_diagnostics_preserve_every_matching_window(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        controller.process = Mock(pid=9468, poll=Mock(return_value=None))
        matching = [
            {"hwnd": index, "is_window": True, "is_visible": True}
            for index in range(32)
        ]
        with (
            patch.object(controller, "_windows_matching_windows", return_value=matching),
            patch.object(smoke, "_windows_rect", side_effect=lambda hwnd: (hwnd, 0, hwnd + 10, 10)),
        ):
            diagnostics = controller._windows_window_diagnostics()
        self.assertEqual(diagnostics["matching_window_count"], len(matching))
        self.assertEqual(len(diagnostics["matching_windows"]), len(matching))
        self.assertNotIn("matching_windows_truncated", diagnostics)

    def test_windows_filetime_conversion_uses_dotnet_datetime_epoch(self) -> None:
        self.assertEqual(
            smoke._windows_filetime_to_datetime_ticks(0),
            "504911232000000000",
        )
        self.assertEqual(
            smoke._windows_filetime_to_datetime_ticks(116444736000000000),
            "621355968000000000",
        )

    def test_windows_launch_records_exact_ui_child_pid_for_supervisor_cleanup(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            marker = smoke.Path(directory) / "native-ui-child.pid"
            process = Mock(pid=731, poll=Mock(return_value=None))
            with (
                patch.dict(smoke.os.environ, {
                    "DOBBYVPN_NATIVE_UI_CHILD_PID_FILE": str(marker),
                }),
                patch.object(smoke.subprocess, "Popen", return_value=process),
                patch.object(smoke, "_windows_rect", return_value=(0, 0, 400, 400)),
                patch.object(smoke.ctypes, "windll", create=True),
            ):
                kernel32 = smoke.ctypes.windll.kernel32
                kernel32.OpenProcess.return_value = 1

                def get_process_times(_handle, creation, _exit, _kernel, _user):
                    # FILETIME zero is the known 1601-01-01 epoch; the marker
                    # must contain the corresponding .NET DateTime ticks.
                    creation._obj.dwLowDateTime = 0
                    creation._obj.dwHighDateTime = 0
                    return True

                kernel32.GetProcessTimes.side_effect = get_process_times
                controller = smoke.NativeUIController(
                    "windows", smoke.Path(directory) / "Dobby Vpn.exe",
                    smoke.Path(directory) / "profile", 1,
                )
                with (
                    patch.object(controller, "_wait"),
                    patch.object(controller, "_windows_validate_window"),
                ):
                    controller._launch_windows()
            self.assertEqual(marker.read_text(encoding="ascii"), "731|504911232000000000")

    def test_windows_click_rejects_exited_child_before_using_retained_hwnd(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        controller.process = Mock(pid=4321, poll=Mock(return_value=1))
        controller.hwnd = 123
        with patch.object(smoke, "_windows_click") as click:
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "process has exited"):
                controller._windows_click_name(smoke._NATIVE_ACTION_LABEL)
        click.assert_not_called()

    def test_windows_close_returns_closed_state_after_clearing_process_handle(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        process = Mock(pid=4321, poll=Mock(return_value=None), returncode=0)
        controller.process = process
        controller.hwnd = 123
        with (
            patch.object(controller, "_windows_key"),
            patch.object(controller, "_wait") as wait,
        ):
            def close_wait(predicate, _message, _timeout=None):
                process.poll.return_value = 0
                self.assertTrue(predicate())

            wait.side_effect = close_wait
            self.assertEqual(
                controller.close(),
                {"status": "Closed", "reconnecting_seen": False},
            )
        self.assertIsNone(controller.process)
        self.assertEqual(controller.hwnd, 0)

    def test_windows_click_rejects_reused_stale_hwnd_before_using_it(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        controller.process = Mock(pid=4321, poll=Mock(return_value=None))
        controller.hwnd = 123
        with (
            patch.object(
                controller,
                "_windows_matching_windows",
                return_value=[{"hwnd": 456, "is_window": True, "is_visible": True}],
            ),
            patch.object(smoke, "_windows_click") as click,
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "stale"):
                controller._windows_click_name(smoke._NATIVE_ACTION_LABEL)
        click.assert_not_called()

    def test_macos_disposable_setup_terminates_preexisting_exact_product_pids(self) -> None:
        identities = (
            smoke._MacOSProcessIdentity(1234, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:34:56 2026"),
            smoke._MacOSProcessIdentity(5678, 501, self._MACOS_EXECUTABLE, "Mon Sep 20 12:35:01 2026"),
        )
        with (
            patch.object(
                smoke,
                "_macos_process_identities",
                return_value=identities,
            ),
            patch.object(smoke, "_macos_identity_is_alive", return_value=False),
            patch.object(smoke, "_terminate_macos_process") as terminate,
        ):
            smoke._terminate_existing_macos_instances(1, self._MACOS_EXECUTABLE)
        self.assertEqual(
            terminate.call_args_list,
            [
                call(identities[0], signal.SIGTERM),
                call(identities[1], signal.SIGTERM),
            ],
        )


class NativeUIControllerProtocolTests(unittest.TestCase):
    def test_windows_capture_uses_owned_window_and_reports_masked_milestone(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = smoke.Path(directory)
            controller = smoke.NativeUIController(
                "windows", root / "ui.exe", root / "profile", 10,
                screenshot_dir=root / "screenshots",
            )
            process = Mock(pid=4321)
            process.poll.return_value = None
            controller.process = process
            controller.hwnd = 99
            with (
                patch.object(controller, "_windows_validate_window"),
                patch.object(smoke, "_windows_rect", return_value=(0, 0, 20, 20)),
                patch.object(controller, "_screenshot_masks", return_value=[(0, 0, 10, 20)]),
                patch.object(smoke, "_windows_window_obstructions", return_value=[]) as obstructions,
                patch.object(
                smoke, "_windows_capture_rect",
                side_effect=lambda rect, path, masks: smoke._write_png(
                    path, 2, 2,
                    b"\xff\x00\x00\xff" + b"\x00\xff\x00\xff"
                    + b"\x00\x00\xff\xff" + b"\xff\xff\xff\xff",
                ),
                ),
            ):
                result = controller.capture("connected")
            self.assertEqual(result["screenshot_width"], 2)
            self.assertEqual(result["masked_regions"], 1)
            self.assertEqual(result["obstructions"], [])
            obstructions.assert_called_once_with(99, 4321)
            screenshot = smoke.Path(str(result["screenshot_path"]))
            self.assertTrue(screenshot.is_file())

    def test_native_ui_smoke_timeout_leaves_response_diagnostic_reserve(self) -> None:
        self.assertEqual(hosted_native_ui._smoke_timeout(120), 90)
        self.assertLess(hosted_native_ui._smoke_timeout(120), 120)
        self.assertLess(hosted_native_ui._smoke_timeout(300), 300)

    def test_native_ui_driver_propagates_inner_timeout_diagnostics(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = smoke.Path(directory)
            process = Mock(pid=4321)
            process.poll.return_value = 1
            process.stdin = None
            process.stdout = None
            process.stderr = None
            controller = hosted_native_ui._NativeUIProcess(
                script=root / "smoke.py",
                platform="windows",
                binary=root / "Dobby Vpn.exe",
                profile=root / "profile",
                timeout=120,
                raw_directory=root,
            )

            def failed_response(_timeout: float) -> dict[str, object]:
                controller._stage = "window-discovery"
                return {
                    "ok": False,
                    "error": "window-discovery={\"child_alive\":true}",
                }

            with (
                patch.object(hosted_native_ui.subprocess, "Popen", return_value=process) as popen,
                patch.object(controller, "_response", side_effect=failed_response),
            ):
                with self.assertRaisesRegex(
                    hosted_native_ui.NativeUIJourneyError,
                    r"window-discovery=.*operation=start, stage=window-discovery",
                ) as raised:
                    controller.start()

            command = popen.call_args.args[0]
            inner = float(command[command.index("--timeout") + 1])
            self.assertLess(inner, controller.timeout)
            self.assertEqual(raised.exception.operation, "start")
            self.assertEqual(raised.exception.stage, "window-discovery")

    def test_native_ui_driver_reports_bounded_start_stage(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            controller = hosted_native_ui._NativeUIProcess(
                script=smoke.Path(directory) / "smoke.py",
                platform="windows",
                binary=smoke.Path(directory) / "Dobby Vpn.exe",
                profile=smoke.Path(directory) / "profile",
                timeout=0.01,
                raw_directory=smoke.Path(directory),
            )
            controller._stage = "window-discovery"
            with self.assertRaisesRegex(
                hosted_native_ui.NativeUIJourneyError,
                r"response timed out \(operation=start, stage=window-discovery\)",
            ):
                controller._response(0.01)

    def test_native_ui_driver_progress_updates_operation_context(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            controller = hosted_native_ui._NativeUIProcess(
                script=smoke.Path(directory) / "smoke.py",
                platform="windows",
                binary=smoke.Path(directory) / "Dobby Vpn.exe",
                profile=smoke.Path(directory) / "profile",
                timeout=1,
                raw_directory=smoke.Path(directory),
            )
            controller._responses.put(json.dumps({
                "ok": True,
                "event": "progress",
                "operation": "connect",
                "stage": "visible-connect",
            }))
            controller._responses.put(json.dumps({"ok": True, "status": "Connected"}))
            self.assertEqual(controller._response(1)["status"], "Connected")
            self.assertEqual(controller._operation, "connect")
            self.assertEqual(controller._stage, "visible-connect")

    def test_macos_configure_returns_input_result_without_status_snapshot(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 10
        )
        events: list[str] = []
        restore = Mock(side_effect=lambda previous: events.append(f"restore:{previous!r}"))
        with (
            patch.object(smoke, "_macos_profile_bytes", return_value=b"profile"),
            patch.object(smoke, "_macos_clipboard_snapshot", return_value=b"previous"),
            patch.object(smoke, "_macos_restore_clipboard", restore),
            patch.object(smoke, "_macos_accessibility_rect", return_value=(1, 2, 100, 200)),
            patch.object(smoke, "_macos_window_rect", return_value=(0, 0, 200, 300)) as window_rect,
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(smoke, "_macos_focus_next") as focus_next,
            patch.object(smoke, "_macos_clipboard_set_verified") as clipboard_set,
            patch.object(smoke, "_macos_keystroke") as keystroke,
            patch.object(smoke, "_macos_copy_selection_verified") as copy_selection,
            patch.object(
                controller,
                "_macos_wait_for_activation",
                side_effect=lambda *_args: events.append("activation"),
            ),
            patch.object(smoke, "_macos_click"),
            patch.object(
                controller,
                "wait_status",
                side_effect=lambda status: events.append(f"status:{status}") or {"status": status},
            ),
            patch.object(controller, "snapshot", side_effect=AssertionError("status snapshot is not part of configure")),
        ):
            self.assertEqual(controller.configure(), {"input_verified": True})
            restore.assert_not_called()
            self.assertEqual(controller.connect(), {"status": "Connected"})
        window_rect.assert_not_called()
        focus_next.assert_called_once_with(4321)
        self.assertEqual(
            keystroke.call_args_list,
            [
                call(4321, "a"),
                call(4321, "v"),
                call(4321, "a"),
                call(4321, "v"),
            ],
        )
        copy_selection.assert_called_once_with(
            smoke._MACOS_INPUT_SENTINEL,
            4321,
            timeout=5,
            control_bounds=(1, 2, 100, 200),
        )
        self.assertEqual(
            clipboard_set.call_args_list,
            [call(smoke._MACOS_INPUT_SENTINEL), call(b"profile")],
        )
        self.assertEqual(events, ["activation", "restore:b'previous'", "status:Connected"])

    def test_macos_reconfigure_restores_abandoned_previous_profile_first(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 10
        )
        abandoned_restore = Mock()
        controller._pending_macos_clipboard_restore = abandoned_restore
        with (
            patch.object(smoke, "_macos_profile_bytes", return_value=b"profile"),
            patch.object(smoke, "_macos_clipboard_snapshot", return_value=b"previous"),
            patch.object(smoke, "_macos_accessibility_rect", return_value=(1, 2, 100, 200)),
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(smoke, "_macos_focus_next"),
            patch.object(smoke, "_macos_clipboard_set_verified"),
            patch.object(smoke, "_macos_keystroke"),
            patch.object(smoke, "_macos_copy_selection_verified"),
            patch.object(smoke, "_macos_restore_clipboard") as restore_clipboard,
        ):
            self.assertEqual(controller.configure(), {"input_verified": True})
        abandoned_restore.assert_called_once_with()
        restore_clipboard.assert_not_called()
        self.assertIsNotNone(controller._pending_macos_clipboard_restore)

    def test_macos_activation_uses_exact_window_title_not_dynamic_child_label(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 10
        )
        with (
            patch.object(smoke, "_macos_window_title", return_value="Dobby VPN — Connecting"),
            patch.object(smoke, "_macos_has_element") as has_element,
        ):
            self.assertEqual(controller._macos_action_state(4321), "Connecting")
        has_element.assert_not_called()

    def test_macos_connect_control_does_not_swallow_ax_failures(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 10
        )
        controller.macos_pid = 4321
        with (
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(
                smoke,
                "_macos_has_element",
                side_effect=smoke.NativeUISmokeError("AX permission failure"),
            ),
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "AX permission failure"):
                controller._connect_control_visible()

        with (
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(
                smoke,
                "_macos_has_element",
                side_effect=smoke.NativeUIElementNotFound("not found"),
            ),
        ):
            self.assertFalse(controller._connect_control_visible())

    def test_macos_wait_status_connected_runs_bounded_ax_title_helper(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 3
        )
        with (
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(
                smoke,
                "_macos_ax_window_title_once",
                return_value="Dobby VPN — Connected",
            ) as title_once,
            patch.object(controller, "snapshot", return_value={"status": "Connected"}),
        ):
            self.assertEqual(
                controller.wait_status("Connected"),
                {"status": "Connected"},
            )
        title_once.assert_called_once()
        self.assertEqual(title_once.call_args.args[0], 4321)
        self.assertGreater(
            title_once.call_args.args[1],
            smoke._MACOS_AX_HELPER_EXIT_RESERVE_SECONDS
            + smoke._MACOS_AX_MESSAGE_TIMEOUT_SECONDS,
        )

    def test_macos_wait_status_rejects_error_from_same_ax_title_read(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 3
        )
        with (
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(
                smoke,
                "_macos_ax_window_title_once",
                return_value="Dobby VPN — Error",
            ) as title_once,
            patch.object(controller, "snapshot") as snapshot,
        ):
            with self.assertRaisesRegex(
                smoke.NativeUISmokeError,
                r"macOS UI reported Error while waiting for Connected",
            ):
                controller.wait_status("Connected")
        title_once.assert_called_once()
        snapshot.assert_not_called()

    def test_process_loss_recovery_reconfigures_and_connects_through_native_ui(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 10
        )
        calls: list[str] = []
        with (
            patch.object(controller, "wait_status") as wait_status,
            patch.object(controller, "_connect_control_visible", return_value=True),
            patch.object(controller, "configure", side_effect=lambda: calls.append("configure") or {}),
            patch.object(controller, "connect", side_effect=lambda: calls.append("connect") or {
                "status": "Connected",
                "reconnecting_seen": True,
            }),
        ):
            result = controller.recover_after_process_loss()
        wait_status.assert_called_once_with("Reconnecting", timeout=2.0)
        self.assertEqual(calls, ["configure", "connect"])
        self.assertEqual(result["status"], "Connected")

    def test_process_loss_recovery_resets_probe_evidence_and_keeps_timeout_context(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 10
        )
        controller._reconnecting_seen = True
        with (
            patch.object(
                controller,
                "wait_status",
                side_effect=smoke.NativeUIWaitTimeout("Reconnecting was not rendered"),
            ),
            patch.object(controller, "_connect_control_visible", return_value=True),
            patch.object(controller, "configure", return_value={}),
            patch.object(controller, "connect", return_value={"status": "Connected"}),
        ):
            result = controller.recover_after_process_loss()
        self.assertFalse(result["reconnecting_seen"])
        self.assertEqual(result["reconnecting_probe_error"], "Reconnecting was not rendered")

    def test_process_loss_recovery_does_not_swallow_native_driver_errors(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui"), smoke.Path("profile"), 10
        )
        with patch.object(
            controller,
            "wait_status",
            side_effect=smoke.NativeUISmokeError("accessibility permission denied"),
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "permission denied"):
                controller.recover_after_process_loss()

    def test_serve_exposes_native_actions_and_preserves_recovery_evidence(self) -> None:
        class FakeController:
            def __init__(self):
                self.closed = 0

            def start(self):
                return {"status": "Disconnected"}

            def snapshot(self):
                return {"status": "Connected", "reconnecting_seen": True}

            def configure(self):
                return {"status": "Disconnected"}

            def connect(self):
                return {"status": "Connected"}

            def disconnect(self):
                return {"status": "Disconnected"}

            def settings(self):
                return {"settings_version": True, "settings_source_commit": True}

            def wait_status(self, state):
                return {"status": state}

            def recover_after_process_loss(self):
                return {
                    "status": "Connected",
                    "reconnecting_seen": True,
                    "ui_reconfigured": True,
                }

            def close(self):
                self.closed += 1
                return {"status": "Disconnected"}

            def close_for_cleanup(self):
                self.closed += 1

        fake = FakeController()
        requests = "\n".join(json.dumps({"op": op}) for op in (
            "configure", "connect", "settings", "process_loss_recovery",
            "disconnect", "close",
        )) + "\n"
        output = io.StringIO()
        with patch.object(smoke, "_controller_for", return_value=fake):
            result = smoke.serve_native_ui(
                "windows", smoke.Path("ui"), smoke.Path("profile"), 1,
                io.StringIO(requests), output,
            )
        self.assertEqual(result, 0)
        responses = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(responses[0]["event"], "progress")
        self.assertEqual(responses[0]["operation"], "start")
        self.assertEqual(responses[0]["stage"], "window-discovery")
        ready = next(response for response in responses if response.get("event") == "ready")
        self.assertTrue(ready["ok"])
        self.assertTrue(responses[-1]["ok"])
        self.assertGreaterEqual(fake.closed, 1)

    def test_serve_preserves_operation_error_without_unbounded_snapshot_diagnostic(self) -> None:
        class FailingOperationController:
            def __init__(self):
                self.snapshots = 0

            def start(self):
                pass

            def snapshot(self):
                self.snapshots += 1
                return {"status": "Disconnected"}

            def configure(self):
                raise smoke.NativeUISmokeError("primary configure sentinel mismatch")

            def close_for_cleanup(self):
                pass

        controller = FailingOperationController()
        output = io.StringIO()
        with patch.object(smoke, "_controller_for", return_value=controller):
            result = smoke.serve_native_ui(
                "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1,
                io.StringIO('{"op":"configure"}\n'), output,
            )
        self.assertEqual(result, 0)
        response = json.loads(output.getvalue().splitlines()[-1])
        self.assertFalse(response["ok"])
        self.assertEqual(response["error"], "primary configure sentinel mismatch")
        self.assertEqual(controller.snapshots, 1)
        self.assertNotIn("snapshot_error", response)

    def test_serve_cleans_startup_failure_before_reporting_it(self) -> None:
        events: list[str] = []

        class FailingController:
            def start(self):
                events.append("start")
                raise smoke.NativeUISmokeError("window discovery failed")

            def close_for_cleanup(self):
                events.append("cleanup")

        output = io.StringIO()
        with patch.object(smoke, "_controller_for", return_value=FailingController()):
            result = smoke.serve_native_ui(
                "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1,
                io.StringIO(), output,
            )
        self.assertEqual(result, 1)
        self.assertEqual(events, ["start", "cleanup"])
        failure = json.loads(output.getvalue().splitlines()[-1])
        self.assertFalse(failure["ok"])
        self.assertIn("window discovery failed", failure["error"])

    def test_serve_terminal_close_cleans_before_ack_without_graceful_close(self) -> None:
        events: list[str] = []

        class TerminalController:
            def __init__(self):
                self.close_calls = 0
                self.cleanup_calls = 0

            def start(self):
                events.append("start")

            def snapshot(self):
                events.append("snapshot")
                return {"status": "Disconnected"}

            def close(self):
                self.close_calls += 1
                events.append("graceful-close")
                raise AssertionError("terminal cleanup must not use graceful close")

            def close_for_cleanup(self):
                self.cleanup_calls += 1
                events.append("cleanup")

        controller = TerminalController()
        output = io.StringIO()
        with patch.object(smoke, "_controller_for", return_value=controller):
            result = smoke.serve_native_ui(
                "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1,
                io.StringIO('{"op":"close"}\n'), output,
            )
        self.assertEqual(result, 0)
        self.assertEqual(controller.close_calls, 0)
        self.assertEqual(controller.cleanup_calls, 1)
        self.assertEqual(events, ["start", "snapshot", "cleanup", "snapshot"])
        response = json.loads(output.getvalue().splitlines()[-1])
        self.assertTrue(response["ok"])

    def test_serve_terminal_close_serializes_cleanup_failure_and_exits(self) -> None:
        class FailingCleanupController:
            def __init__(self):
                self.cleanup_calls = 0

            def start(self):
                pass

            def snapshot(self):
                return {"status": "Disconnected"}

            def close_for_cleanup(self):
                self.cleanup_calls += 1
                raise smoke.NativeUISmokeError("verified product process did not exit")

        controller = FailingCleanupController()
        output = io.StringIO()
        with patch.object(smoke, "_controller_for", return_value=controller):
            result = smoke.serve_native_ui(
                "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1,
                io.StringIO('{"op":"close"}\n'), output,
            )
        self.assertEqual(result, 1)
        self.assertEqual(controller.cleanup_calls, 1)
        response = json.loads(output.getvalue().splitlines()[-1])
        self.assertFalse(response["ok"])
        self.assertIn("did not exit", response["error"])

    def test_serve_terminal_close_reports_snapshot_failure_after_cleanup(self) -> None:
        class SnapshotFailureController:
            def __init__(self):
                self.snapshots = 0

            def start(self):
                pass

            def snapshot(self):
                self.snapshots += 1
                if self.snapshots == 1:
                    return {"status": "Disconnected"}
                raise smoke.NativeUISmokeError("post-cleanup AX snapshot failed")

            def close_for_cleanup(self):
                pass

        output = io.StringIO()
        with patch.object(smoke, "_controller_for", return_value=SnapshotFailureController()):
            result = smoke.serve_native_ui(
                "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1,
                io.StringIO('{"op":"close"}\n'), output,
            )
        self.assertEqual(result, 1)
        response = json.loads(output.getvalue().splitlines()[-1])
        self.assertFalse(response["ok"])
        self.assertIn("post-cleanup AX snapshot failed", response["snapshot_error"])


class NativeUIClipboardCleanupTests(unittest.TestCase):
    def test_windows_paste_restores_previous_text_without_exposing_profile(self) -> None:
        previous = "user clipboard"
        profile_text = "vpn-profile-secret"
        calls: list[tuple[list[str], dict[str, object]]] = []

        def run(command, **kwargs):
            calls.append((command, kwargs))
            if len(calls) == 1:
                return subprocess.CompletedProcess(command, 0, stdout=base64.b64encode(previous.encode()).decode(), stderr="")
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            profile = smoke.Path(directory) / "profile.conf"
            profile.write_text(profile_text, encoding="utf-8")
            with patch.object(smoke.shutil, "which", return_value="powershell"), \
                    patch.object(smoke.subprocess, "run", side_effect=run):
                restore = smoke._windows_paste(profile)
                restore()

        self.assertEqual(len(calls), 3)
        self.assertEqual(calls[2][1]["input"], base64.b64encode(previous.encode()).decode())
        self.assertNotIn(profile_text, repr(calls))

    def test_windows_cleanup_falls_back_to_empty_clipboard(self) -> None:
        calls: list[tuple[list[str], dict[str, object]]] = []

        def run(command, **kwargs):
            calls.append((command, kwargs))
            if len(calls) == 1:
                return subprocess.CompletedProcess(command, 1, stdout="", stderr="ignored")
            if len(calls) == 3:
                return subprocess.CompletedProcess(command, 1, stdout="", stderr="ignored")
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as directory:
            profile = smoke.Path(directory) / "profile.conf"
            profile.write_text("vpn-profile-secret", encoding="utf-8")
            with patch.object(smoke.shutil, "which", return_value="powershell"), \
                patch.object(smoke.subprocess, "run", side_effect=run):
                restore = smoke._windows_paste(profile)
                with self.assertRaisesRegex(smoke.NativeUISmokeError, "clipboard was cleared"):
                    restore()

        self.assertEqual(len(calls), 4)
        self.assertEqual(calls[-1][1]["input"], "")
        self.assertIn("[System.Windows.Forms.Clipboard]::Clear()", calls[-1][0][-1])

    def test_macos_profile_validation_rejects_nul_and_invalid_utf8(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            profile = smoke.Path(directory) / "profile.conf"
            profile.write_bytes(b"ok\x00bad")
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "contains NUL"):
                smoke._macos_profile_bytes(profile)
            profile.write_bytes(b"ok\xffbad")
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "not UTF-8"):
                smoke._macos_profile_bytes(profile)

    def test_macos_clipboard_set_verification_compares_bytes_without_logging(self) -> None:
        value = b"synthetic-profile"
        with (
            patch.object(smoke, "_macos_set_clipboard") as set_clipboard,
            patch.object(smoke, "_macos_clipboard_snapshot", return_value=value),
        ):
            smoke._macos_clipboard_set_verified(value)
        set_clipboard.assert_called_once_with(value)

    def test_macos_clipboard_restore_reports_restore_and_clear_failures(self) -> None:
        with patch.object(
            smoke,
            "_macos_set_clipboard",
            side_effect=[
                smoke.NativeUISmokeError("restore failed"),
                smoke.NativeUISmokeError("clear failed"),
            ],
        ) as set_clipboard:
            with self.assertRaisesRegex(
                smoke.NativeUISmokeError,
                r"restore_error=restore failed; clear_error=clear failed",
            ):
                smoke._macos_restore_clipboard(b"previous")
        self.assertEqual(set_clipboard.call_args_list, [call(b"previous"), call(b"")])

    def test_macos_clipboard_restore_reports_primary_failure_when_clear_succeeds(self) -> None:
        with patch.object(
            smoke,
            "_macos_set_clipboard",
            side_effect=[smoke.NativeUISmokeError("restore failed"), None],
        ) as set_clipboard:
            with self.assertRaisesRegex(
                smoke.NativeUISmokeError,
                r"clipboard was cleared \(restore_error=restore failed\)",
            ):
                smoke._macos_restore_clipboard(b"previous")
        self.assertEqual(set_clipboard.call_args_list, [call(b"previous"), call(b"")])

    def test_macos_pasteboard_change_count_parses_jxa_output(self) -> None:
        result = subprocess.CompletedProcess(
            ["osascript"], 0, stdout="42\n", stderr=""
        )
        with patch.object(smoke.subprocess, "run", return_value=result) as run:
            self.assertEqual(smoke._macos_pasteboard_change_count(), 42)
        self.assertEqual(run.call_args.args[0][:3], ["osascript", "-l", "JavaScript"])

    def test_macos_copy_selection_waits_for_generation_and_expected_bytes(self) -> None:
        expected = b"synthetic-profile"
        with (
            patch.object(smoke, "_macos_keystroke") as keystroke,
            patch.object(smoke, "_macos_clipboard_set_verified") as set_clipboard,
            patch.object(smoke, "_macos_pasteboard_change_count", side_effect=[10, 11]),
            patch.object(smoke, "_macos_clipboard_snapshot", return_value=expected),
        ):
            smoke._macos_copy_selection_verified(expected, 4321, timeout=1)
        self.assertEqual(keystroke.call_args_list, [call(4321, "a"), call(4321, "c")])
        set_clipboard.assert_not_called()

    def test_macos_copy_selection_does_not_read_clipboard_before_generation_changes(self) -> None:
        expected = b"already-on-clipboard"
        with (
            patch.object(smoke, "_macos_keystroke"),
            patch.object(smoke, "_macos_pasteboard_change_count", return_value=10),
            patch.object(smoke, "_macos_clipboard_snapshot", return_value=expected) as snapshot,
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "pasteboard_changed=false"):
                smoke._macos_copy_selection_verified(expected, 4321, timeout=0.1)
        snapshot.assert_not_called()

    def test_macos_copy_selection_reports_changed_wrong_bytes_without_exposing_them(self) -> None:
        expected = b"expected-profile"
        wrong = b"wrong-profile"
        with (
            patch.object(smoke, "_macos_keystroke"),
            patch.object(smoke, "_macos_pasteboard_change_count", side_effect=[10, *([11] * 20)]),
            patch.object(smoke, "_macos_clipboard_snapshot", return_value=wrong),
            patch.object(smoke, "_macos_frontmost_pid", return_value=4321),
        ):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "observed_bytes=13"):
                smoke._macos_copy_selection_verified(expected, 4321, timeout=0.1)

    def test_macos_copy_selection_accepts_delayed_expected_bytes_after_generation_change(self) -> None:
        expected = b"expected-profile"
        with (
            patch.object(smoke, "_macos_keystroke"),
            patch.object(smoke, "_macos_pasteboard_change_count", side_effect=[10, *([11] * 20)]),
            patch.object(smoke, "_macos_clipboard_snapshot", side_effect=[b"old", expected]),
        ):
            smoke._macos_copy_selection_verified(expected, 4321, timeout=1)

    def test_configure_restores_clipboard_when_native_input_fails(self) -> None:
        controller = smoke.NativeUIController("windows", smoke.Path("ui"), smoke.Path("profile"), 1)
        restore = Mock()
        with (
            patch.object(smoke, "_windows_paste", return_value=restore),
            patch.object(
                controller,
                "_windows_click_name",
                side_effect=smoke.NativeUISmokeError("input failed"),
            ),
        ):
            with self.assertRaises(smoke.NativeUISmokeError):
                controller.configure()
        restore.assert_called_once_with()

    def test_macos_configure_failure_preserves_input_and_restore_errors(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 10
        )
        restore = Mock(side_effect=smoke.NativeUISmokeError("restore failed"))
        with (
            patch.object(smoke, "_macos_profile_bytes", return_value=b"profile"),
            patch.object(smoke, "_macos_clipboard_snapshot", return_value=b"previous"),
            patch.object(smoke, "_macos_restore_clipboard", restore),
            patch.object(smoke, "_macos_accessibility_rect", return_value=(1, 2, 100, 200)),
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(smoke, "_macos_focus_next"),
            patch.object(smoke, "_macos_clipboard_set_verified"),
            patch.object(
                smoke,
                "_macos_keystroke",
                side_effect=[None, None, smoke.NativeUISmokeError("input failed")],
            ),
            patch.object(smoke, "_macos_copy_selection_verified"),
        ):
            with self.assertRaisesRegex(
                smoke.NativeUISmokeError,
                r"input failed; macOS clipboard restore also failed: restore failed",
            ):
                controller.configure()
        restore.assert_called_once_with(b"previous")
        controller._restore_pending_macos_clipboard()

    def test_macos_connect_failure_preserves_activation_and_restore_errors(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 10
        )
        restore = Mock(side_effect=smoke.NativeUISmokeError("restore failed"))
        controller._pending_macos_clipboard_restore = restore
        with (
            patch.object(controller, "_macos_pid_or_error", return_value=4321),
            patch.object(smoke, "_macos_accessibility_rect", return_value=(1, 2, 100, 200)),
            patch.object(smoke, "_macos_click"),
            patch.object(
                controller,
                "_macos_wait_for_activation",
                side_effect=smoke.NativeUISmokeError("activation failed"),
            ),
        ):
            with self.assertRaisesRegex(
                smoke.NativeUISmokeError,
                r"activation failed; macOS clipboard restore also failed: restore failed",
            ):
                controller.connect()
        restore.assert_called_once_with()
        controller._restore_pending_macos_clipboard()

    def test_macos_cleanup_restores_abandoned_clipboard_once(self) -> None:
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1
        )
        restore = Mock()
        controller._pending_macos_clipboard_restore = restore
        controller.close_for_cleanup()
        controller.close_for_cleanup()
        restore.assert_called_once_with()

    def test_macos_cleanup_preserves_cleanup_and_restore_errors(self) -> None:
        identity = smoke._MacOSProcessIdentity(
            4321, 501, "/tmp/Dobby Vpn.app/Contents/MacOS/Dobby Vpn", "start"
        )
        process = Mock(pid=9001, poll=Mock(return_value=None))
        controller = smoke.NativeUIController(
            "macos", smoke.Path("ui.app"), smoke.Path("profile"), 1
        )
        controller.process = process
        controller.macos_pid = identity.pid
        controller.macos_process_identity = identity
        controller._pending_macos_clipboard_restore = Mock(
            side_effect=smoke.NativeUISmokeError("restore failed")
        )
        with patch.object(
            smoke,
            "_terminate_macos_process_tree",
            side_effect=smoke.NativeUISmokeError("cleanup failed"),
        ):
            with self.assertRaisesRegex(
                smoke.NativeUISmokeError,
                r"cleanup failed; macOS clipboard restore also failed: restore failed",
            ):
                controller.close_for_cleanup()
        process.wait.assert_called_once_with(timeout=2)

    def test_configure_replaces_existing_profile_before_native_paste(self) -> None:
        controller = smoke.NativeUIController(
            "windows", smoke.Path("ui"), smoke.Path("profile"), 1
        )
        restore = Mock()
        keys: list[tuple[int, ...]] = []
        with (
            patch.object(smoke, "_windows_paste", return_value=restore),
            patch.object(controller, "_windows_click_name"),
            patch.object(controller, "_windows_key", side_effect=lambda *values: keys.append(values)),
        ):
            controller.configure()
        self.assertEqual(keys, [(0x11, 0x41), (0x11, 0x56)])
        restore.assert_called_once_with()


if __name__ == "__main__":
    unittest.main()
