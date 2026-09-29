from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from io import BytesIO, StringIO
import importlib.util
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock


PRODUCT_ROOT = Path(__file__).resolve().parents[2]
DESKTOP_PACKAGE_PATH = PRODUCT_ROOT / ".github/scripts/desktop/desktop_package.py"
SPEC = importlib.util.spec_from_file_location(
    "dobbyvpn_desktop_package_test", DESKTOP_PACKAGE_PATH
)
if SPEC is None or SPEC.loader is None:
    raise AssertionError(f"could not load {DESKTOP_PACKAGE_PATH}")
desktop_package = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = desktop_package
SPEC.loader.exec_module(desktop_package)


class BinaryCapture:
    def __init__(self) -> None:
        self.buffer = BytesIO()
        self.text = StringIO()

    def write(self, value: str) -> int:
        return self.text.write(value)

    def flush(self) -> None:
        self.text.flush()


class DesktopPlatformTimeoutTests(unittest.TestCase):
    def test_timeout_forwards_partial_output_and_reports_exception(self) -> None:
        stdout = b"partial PowerShell output \xff\n"
        stderr = b"COM query warning \x00\n"
        output_capture = BinaryCapture()
        error_capture = BinaryCapture()
        timeout = subprocess.TimeoutExpired(
            ["powershell.exe"],
            180,
            output=stdout,
            stderr=stderr,
        )

        with mock.patch.object(desktop_package.subprocess, "run", side_effect=timeout) as run:
            with redirect_stdout(output_capture), redirect_stderr(error_capture):
                with self.assertRaises(desktop_package.DesktopPlatformError) as failure:
                    desktop_package._run(
                        "Windows MSI content and version check",
                        ["powershell.exe"],
                        capture=True,
                        timeout_seconds=180,
                    )

        self.assertEqual(output_capture.buffer.getvalue(), stdout)
        self.assertEqual(error_capture.buffer.getvalue(), stderr)
        self.assertIn("timed out after 180 seconds", str(failure.exception))
        self.assertIsInstance(failure.exception.__cause__, subprocess.TimeoutExpired)
        self.assertEqual(run.call_args.kwargs["timeout"], 180)

    def test_msi_verification_uses_bounded_timeout(self) -> None:
        with mock.patch.object(desktop_package, "_run") as run:
            desktop_package._verify_msi(Path("package.msi"), "1.2.3", {})

        self.assertEqual(
            run.call_args.kwargs["timeout_seconds"],
            desktop_package.WINDOWS_MSI_VERIFY_TIMEOUT_SECONDS,
        )


if __name__ == "__main__":
    unittest.main()
