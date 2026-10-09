from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from io import BytesIO, StringIO
import argparse
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


PRODUCT_ROOT = Path(__file__).resolve().parents[4]
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


class WindowsTempPreflightTests(unittest.TestCase):
    def _build_args(self, output_dir: Path) -> argparse.Namespace:
        return argparse.Namespace(
            platform="windows",
            arch="amd64",
            version="1.2.3",
            source_sha="0123456789abcdef0123456789abcdef01234567",
            output_dir=output_dir,
            debug=False,
            skip_deps=True,
        )

    def test_failed_temp_preflight_prevents_backend_and_ui_builds(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            with mock.patch.object(desktop_package, "_select_host", return_value=("windows", "amd64")), \
                    mock.patch.object(desktop_package, "_version", return_value="1.2.3"), \
                    mock.patch.object(desktop_package, "_source_sha", return_value="0123456789abcdef0123456789abcdef01234567"), \
                    mock.patch.object(desktop_package, "_windows_temp_preflight", side_effect=desktop_package.DesktopPlatformError("temp unavailable")), \
                    mock.patch.object(desktop_package, "_run") as run:
                with self.assertRaisesRegex(desktop_package.DesktopPlatformError, "temp unavailable"):
                    desktop_package._build(self._build_args(Path(temporary) / "out"))
            self.assertFalse((Path(temporary) / "out").exists())

        run.assert_not_called()

    def test_successful_temp_preflight_precedes_backend_and_ui_builds(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "out"
            events: list[str] = []

            def build_msi(*args: object) -> Path:
                work = args[4]
                assert isinstance(work, Path)
                package = work / "test.msi"
                package.write_bytes(b"test MSI")
                return package

            with mock.patch.object(desktop_package, "_select_host", return_value=("windows", "amd64")), \
                    mock.patch.object(desktop_package, "_version", return_value="1.2.3"), \
                    mock.patch.object(desktop_package, "_source_sha", return_value="0123456789abcdef0123456789abcdef01234567"), \
                    mock.patch.object(desktop_package, "_windows_temp_preflight", side_effect=lambda _env: events.append("temp preflight")), \
                    mock.patch.object(desktop_package, "_run", side_effect=lambda label, *_args, **_kwargs: events.append(label)), \
                    mock.patch.object(desktop_package, "_ensure_windows_pillow", side_effect=lambda _work, env: env), \
                    mock.patch.object(desktop_package, "_build_windows_msi", side_effect=build_msi):
                self.assertEqual(desktop_package._build(self._build_args(output)), 0)
            self.assertTrue((output / "dobbyVPN-windows-amd64.msi").is_file())

        self.assertEqual(
            events[:4],
            [
                "temp preflight",
                "Go backend and CLI build",
                "native desktop UI build",
                "desktop application payload assembly",
            ],
        )

    def test_cli_preflight_is_windows_only(self) -> None:
        with mock.patch.object(desktop_package, "_host", return_value=("linux", "amd64")), \
                mock.patch.object(desktop_package, "_run") as run:
            with self.assertRaisesRegex(desktop_package.DesktopPlatformError, "must be built on a windows runner"):
                desktop_package.main(["preflight-windows-temp"])

        run.assert_not_called()

    def test_cli_preflight_forwards_failed_probe_output_bytes(self) -> None:
        stdout = b"identity and temp candidates \xff\n"
        stderr = b"temp probe failure \x00\n"
        completed = subprocess.CompletedProcess(
            ["powershell.exe"], 1, stdout=stdout, stderr=stderr
        )
        output_capture = BinaryCapture()
        error_capture = BinaryCapture()

        with mock.patch.object(desktop_package, "_select_host", return_value=("windows", "amd64")), \
                mock.patch.object(desktop_package.subprocess, "run", return_value=completed) as run:
            with redirect_stdout(output_capture), redirect_stderr(error_capture):
                with self.assertRaisesRegex(desktop_package.DesktopPlatformError, "command exited 1"):
                    desktop_package.main(["preflight-windows-temp"])

        self.assertEqual(output_capture.buffer.getvalue(), stdout)
        self.assertEqual(error_capture.buffer.getvalue(), stderr)
        self.assertEqual(run.call_args.kwargs["timeout"], desktop_package.WINDOWS_TEMP_PREFLIGHT_TIMEOUT_SECONDS)
        self.assertEqual(run.call_args.kwargs["stdout"], subprocess.PIPE)
        self.assertEqual(run.call_args.kwargs["stderr"], subprocess.PIPE)


if __name__ == "__main__":
    unittest.main()
