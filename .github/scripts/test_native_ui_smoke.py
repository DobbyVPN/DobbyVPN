from __future__ import annotations

import subprocess
import unittest
from unittest.mock import patch

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


if __name__ == "__main__":
    unittest.main()
