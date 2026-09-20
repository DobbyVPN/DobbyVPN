from __future__ import annotations

import io
import json
import base64
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

import native_ui_smoke as smoke


class NativeUISmokeIdentityTests(unittest.TestCase):
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
        console = subprocess.CompletedProcess(["stat"], 0, stdout="alice\n", stderr="")
        with patch.object(smoke.getpass, "getuser", return_value="runner"), \
                patch.object(smoke.subprocess, "run", return_value=console):
            with self.assertRaisesRegex(smoke.NativeUISmokeError, "does not own the console"):
                smoke._macos_interactive_identity()


class NativeUIControllerProtocolTests(unittest.TestCase):
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

            def wait_recovered(self):
                return {"status": "Connected", "reconnecting_seen": True}

            def close(self):
                self.closed += 1
                return {"status": "Disconnected"}

            def close_for_cleanup(self):
                self.closed += 1

        fake = FakeController()
        requests = "\n".join(json.dumps({"op": op}) for op in (
            "configure", "connect", "settings", "recovery", "disconnect", "close",
        )) + "\n"
        output = io.StringIO()
        with patch.object(smoke, "_controller_for", return_value=fake):
            result = smoke.serve_native_ui(
                "windows", smoke.Path("ui"), smoke.Path("profile"), 1,
                io.StringIO(requests), output,
            )
        self.assertEqual(result, 0)
        responses = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(responses[0]["event"], "ready")
        self.assertTrue(responses[-1]["ok"])
        self.assertGreaterEqual(fake.closed, 1)


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
                restore()

        self.assertEqual(len(calls), 4)
        self.assertEqual(calls[-1][1]["input"], "")

    def test_macos_paste_restores_previous_bytes_without_exposing_profile(self) -> None:
        previous = b"user clipboard"
        profile_bytes = b"vpn-profile-secret"
        calls: list[tuple[list[str], dict[str, object]]] = []

        def run(command, **kwargs):
            calls.append((command, kwargs))
            if command == ["pbpaste"]:
                return subprocess.CompletedProcess(command, 0, stdout=previous, stderr=b"")
            return subprocess.CompletedProcess(command, 0, stdout=b"", stderr=b"")

        with tempfile.TemporaryDirectory() as directory:
            profile = smoke.Path(directory) / "profile.conf"
            profile.write_bytes(profile_bytes)
            with patch.object(smoke.subprocess, "run", side_effect=run):
                restore = smoke._macos_paste(profile)
                restore()

        self.assertEqual(calls[0][0], ["pbpaste"])
        self.assertEqual(calls[-1][1]["input"], previous)
        self.assertEqual([call[0] for call in calls], [["pbpaste"], ["pbcopy"], ["pbcopy"]])

    def test_configure_restores_clipboard_when_native_input_fails(self) -> None:
        controller = smoke.NativeUIController("windows", smoke.Path("ui"), smoke.Path("profile"), 1)
        restore = Mock()
        with patch.object(smoke, "_windows_paste", return_value=restore), \
                patch.object(controller, "_windows_click_name", side_effect=smoke.NativeUISmokeError("input failed")):
            with self.assertRaises(smoke.NativeUISmokeError):
                controller.configure()
        restore.assert_called_once_with()


if __name__ == "__main__":
    unittest.main()
