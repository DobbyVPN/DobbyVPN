from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from torturer_runner import local_vm, local_vm_windows


class WindowsReleaseInstallTests(unittest.TestCase):
    def test_control_sid_resolves_configured_account_without_interpolating_it(self) -> None:
        command = ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "script"]
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.dict(os.environ, {"DOBBYVPN_CONTROL_PIPE_USER": r"Dobby Lab\runner"}), \
                mock.patch.object(
                    local_vm,
                    "_run_logged",
                    return_value=subprocess.CompletedProcess(
                        command, 0, b"S-1-5-21-123-456-789-1001\r\n", b"",
                    ),
                ) as run_logged:
            sid = local_vm._windows_control_pipe_sid(
                Path(directory), Path(directory) / "logs", 30,
            )

        self.assertEqual(sid, "S-1-5-21-123-456-789-1001")
        invocation = run_logged.call_args.args[0]
        self.assertEqual(invocation[:4], ["powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive"])
        self.assertNotIn("Dobby Lab", invocation[-1])
        self.assertEqual(
            run_logged.call_args.kwargs["environment"]["DOBBYVPN_CONTROL_PIPE_USER"],
            r"Dobby Lab\runner",
        )

    def test_exact_windows_msi_receives_resolved_control_sid(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            package = root / "dobbyVPN-windows-amd64.msi"
            package.write_bytes(b"test msi")
            logs = root / "logs"
            calls: list[list[str]] = []

            def run_logged(command: list[str], **kwargs: object) -> subprocess.CompletedProcess[bytes]:
                calls.append(command)
                if command[0] == "msiexec.exe":
                    return subprocess.CompletedProcess(command, 0, b"", b"")
                return subprocess.CompletedProcess(
                    command,
                    0,
                    b"C:\\Program Files\\DobbyVPN\\dobby-cli.exe|"
                    b"C:\\Program Files\\DobbyVPN\\dobbyvpn-backend.exe|"
                    b"C:\\Program Files\\DobbyVPN\\bin\\DobbyVPN.exe",
                    b"",
                )

            with mock.patch.object(
                local_vm,
                "_windows_control_pipe_sid",
                return_value="S-1-5-21-123-456-789-1001",
            ), mock.patch.object(local_vm, "_run_logged", side_effect=run_logged):
                local_vm._install_windows_release(
                    root,
                    {
                        "repository": "DobbyVPN/DobbyVPN",
                        "workflow": ".github/workflows/release.yml",
                        "run_id": "123456",
                        "source_sha": "a" * 40,
                        "platform": "windows",
                    },
                    {("package", "amd64"): package},
                    logs,
                    60,
                )

            self.assertIn(
                "DOBBYVPN_CONTROL_PIPE_SID=S-1-5-21-123-456-789-1001",
                calls[0],
            )

    def test_interactive_native_ui_can_import_prepared_screenshot_decoder(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            run_dir = Path(directory)
            source = run_dir / "source"
            cwd = source / "torturer"
            logs = run_dir / "logs"
            profile = run_dir / "profile"
            python_target = run_dir / "screenshot-python"
            for path in (cwd, logs, profile, python_target):
                path.mkdir(parents=True)

            scripts: dict[str, str] = {}

            def run_powershell(script: str, **kwargs: object) -> subprocess.CompletedProcess[bytes]:
                label = str(kwargs["label"])
                scripts[label] = script
                if label == "native-ui-access":
                    scripts["native-ui-wrapper"] = (
                        run_dir / "native-ui-task.ps1"
                    ).read_text(encoding="utf-8")
                if label == "native-ui-task-register":
                    (run_dir / "native-ui.exit").write_text("0\n", encoding="ascii")
                return subprocess.CompletedProcess(["powershell.exe"], 0, b"", b"")

            environment = {
                "DOBBYVPN_CONTROL_PIPE_USER": r"Dobby Lab\runner",
                "PYTHONPATH": str(python_target),
            }
            command = [
                r"C:\Windows\py.exe", "-3", "-m", "torturer_runner.ui.journey",
                "--ui", r"C:\candidate\DobbyVPN.exe",
            ]
            with (
                mock.patch.object(local_vm_windows, "_preflight_interactive_desktop"),
                mock.patch.object(local_vm_windows, "_powershell", side_effect=run_powershell),
            ):
                result = local_vm_windows.run_interactive_task(
                    command,
                    run_dir=run_dir,
                    cwd=cwd,
                    logs=logs,
                    timeout=1,
                    environment=environment,
                    task_label="native-ui",
                )

            self.assertEqual(result.returncode, 0)
            self.assertIn(
                f"$info.EnvironmentVariables['PYTHONPATH'] = '{python_target}'",
                scripts["native-ui-wrapper"],
            )
            self.assertIn(
                f"Grant-Access '{python_target}' '(OI)(CI)RX' $true",
                scripts["native-ui-access"],
            )


if __name__ == "__main__":
    unittest.main()
