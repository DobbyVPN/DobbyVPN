from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from io import BytesIO, StringIO
import argparse
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
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


class WindowsMSIFailureTests(unittest.TestCase):
    def test_failed_compression_preserves_bytes_and_inspects_missing_scratch_parent(self) -> None:
        output_capture = BinaryCapture()
        error_capture = BinaryCapture()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "payload.zip"
            archive.write_bytes(b"synthetic archive")
            service = root / "service"
            service.mkdir()
            for name in ("dobbyvpn-backend.exe", "dobby_bridge.dll", "wintun.dll"):
                (service / name).write_bytes(b"synthetic input")
            cabinet = root / "missing-scratch" / "cab1.cab"
            original_stdout = b"original WiX output \xff\n"
            original_stderr = f"failed to compress cabinet: {cabinet}\n".encode()
            results = [
                subprocess.CompletedProcess(["dotnet"], 0),
                subprocess.CompletedProcess(["cmd.exe"], 1, original_stdout, original_stderr),
            ]
            with mock.patch.object(desktop_package.subprocess, "run", side_effect=results), \
                    mock.patch.object(desktop_package, "_verify_msi") as verify, \
                    redirect_stdout(output_capture), redirect_stderr(error_capture):
                with self.assertRaisesRegex(desktop_package.DesktopPlatformError, "Windows MSI build: command exited 1"):
                    desktop_package._build_windows_msi(archive, service, "1.5.4", "0" * 40, root, {})
            self.assertIn(f"path={cabinet.parent} inspection error=", output_capture.text.getvalue())
            self.assertIn("dobbyvpn-backend.exe length=", output_capture.text.getvalue())
            self.assertEqual(output_capture.buffer.getvalue(), original_stdout)
            self.assertEqual(error_capture.buffer.getvalue(), original_stderr)
            verify.assert_not_called()


class WindowsTempPreflightTests(unittest.TestCase):
    class NativeTempPath:
        def __init__(self, path: str = "", *, result: int | None = None) -> None:
            self.path = path
            self.result = result
            self.capacities: list[int] = []

        def __call__(self, capacity: int, buffer: object) -> int:
            self.capacities.append(capacity)
            if self.result == 0:
                return 0
            setattr(buffer, "value", self.path)
            return self.result if self.result is not None else len(self.path)

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

    def test_native_temp_paths_use_bounded_buffers_and_only_fallback_when_missing(self) -> None:
        legacy = self.NativeTempPath("C:\\Windows\\Temp\\")
        modern = self.NativeTempPath("C:\\Windows\\SystemTemp\\")
        with redirect_stdout(StringIO()):
            paths = desktop_package._windows_temp_paths(
                SimpleNamespace(GetTempPathW=legacy, GetTempPath2W=modern)
            )
        self.assertEqual(legacy.capacities, [32768])
        self.assertEqual(modern.capacities, [32768])
        self.assertEqual(paths, ("C:\\Windows\\Temp\\", "C:\\Windows\\SystemTemp\\"))

        missing_modern = self.NativeTempPath("C:\\Windows\\Temp\\")
        with redirect_stdout(StringIO()):
            fallback = desktop_package._windows_temp_paths(
                SimpleNamespace(GetTempPathW=missing_modern)
            )
        self.assertEqual(fallback, ("C:\\Windows\\Temp\\", None))

    def test_native_temp_path_failure_is_not_treated_as_missing_entrypoint(self) -> None:
        legacy = self.NativeTempPath("C:\\Windows\\Temp\\")
        modern = self.NativeTempPath(result=0)
        output = StringIO()
        with mock.patch.object(
            desktop_package, "_windows_api_error", side_effect=OSError("native path failure")
        ):
            with redirect_stdout(output):
                with self.assertRaisesRegex(OSError, "native path failure"):
                    desktop_package._windows_temp_paths(
                        SimpleNamespace(GetTempPathW=legacy, GetTempPath2W=modern)
                    )
        self.assertEqual(output.getvalue(), "GetTempPathW=C:\\Windows\\Temp\\\n")

    def test_temp_candidates_deduplicate_case_insensitively(self) -> None:
        self.assertEqual(
            desktop_package._normalized_temp_candidates(
                [
                    ("GetTempPathW", "C:\\WINDOWS\\TEMP\\"),
                    ("GetTempPath2W", "c:\\windows\\temp"),
                ]
            ),
            [("C:\\WINDOWS\\TEMP", ["GetTempPathW", "GetTempPath2W"])],
        )

    def test_whoami_output_bytes_are_forwarded_when_identity_is_parsed(self) -> None:
        stdout = b'"MACHINE\\SYSTEM","S-1-5-18"\r\n'
        stderr = b"original whoami warning \xff\n"
        output_capture = BinaryCapture()
        error_capture = BinaryCapture()
        completed = subprocess.CompletedProcess(
            ["whoami.exe"], 0, stdout=stdout, stderr=stderr
        )

        with mock.patch.object(
            desktop_package.subprocess, "run", return_value=completed
        ), redirect_stdout(output_capture), redirect_stderr(error_capture):
            identity = desktop_package._windows_identity_details()

        self.assertEqual(identity, ("MACHINE\\SYSTEM", "S-1-5-18"))
        self.assertEqual(output_capture.buffer.getvalue(), stdout)
        self.assertEqual(error_capture.buffer.getvalue(), stderr)

    def test_whoami_output_bytes_are_forwarded_when_identity_output_is_malformed(self) -> None:
        stdout = b"malformed identity output \xff\n"
        stderr = b"original whoami diagnostic \xfe\n"
        output_capture = BinaryCapture()
        error_capture = BinaryCapture()
        completed = subprocess.CompletedProcess(
            ["whoami.exe"], 0, stdout=stdout, stderr=stderr
        )

        with mock.patch.object(
            desktop_package.subprocess, "run", return_value=completed
        ), redirect_stdout(output_capture), redirect_stderr(error_capture):
            with self.assertRaisesRegex(ValueError, "expected account and SID"):
                desktop_package._windows_identity_details()

        self.assertEqual(output_capture.buffer.getvalue(), stdout)
        self.assertEqual(error_capture.buffer.getvalue(), stderr)

    def test_temp_candidate_preserves_primary_and_all_cleanup_errors(self) -> None:
        with (
            mock.patch.object(
                desktop_package.os, "stat", return_value=SimpleNamespace(st_mode=0o040000)
            ),
            mock.patch.object(
                desktop_package.shutil, "disk_usage", return_value=SimpleNamespace(free=1024)
            ),
            mock.patch.object(desktop_package.os, "open", return_value=11),
            mock.patch.object(
                desktop_package.os, "write", side_effect=OSError("primary write failure")
            ),
            mock.patch.object(
                desktop_package.os, "close", side_effect=OSError("close cleanup failure")
            ) as close,
            mock.patch.object(
                desktop_package.os, "unlink", side_effect=OSError("delete cleanup failure")
            ) as unlink,
            redirect_stdout(StringIO()),
        ):
            failures = desktop_package._probe_windows_temp_candidate(
                r"C:\Windows\Temp", ["GetTempPathW"]
            )

        self.assertEqual(
            [failure[0] for failure in failures],
            [
                "Temp candidate GetTempPathW primary failure",
                "Temp candidate GetTempPathW cleanup failure",
                "Temp candidate GetTempPathW cleanup failure",
            ],
        )
        self.assertEqual(
            [str(failure[1]) for failure in failures],
            [
                "primary write failure",
                "close cleanup failure",
                "delete cleanup failure",
            ],
        )
        close.assert_called_once_with(11)
        unlink.assert_called_once()

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

    def test_temp_preflight_reuses_python_environment_and_deadline(self) -> None:
        environment = {"TEMP": "C:\\Windows\\Temp"}
        with mock.patch.object(desktop_package, "_run") as run:
            desktop_package._windows_temp_preflight(environment)

        self.assertEqual(run.call_args.args[1][0], sys.executable)
        self.assertIs(run.call_args.kwargs["env"], environment)
        self.assertEqual(
            run.call_args.kwargs["timeout_seconds"],
            desktop_package.WINDOWS_TEMP_PREFLIGHT_TIMEOUT_SECONDS,
        )

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
        output_capture = BinaryCapture()
        error_capture = BinaryCapture()

        def fail_probe(
            command: list[str], **_kwargs: object
        ) -> subprocess.CompletedProcess[bytes]:
            sys.stdout.buffer.write(stdout)
            sys.stderr.buffer.write(stderr)
            return subprocess.CompletedProcess(command, 1)

        with mock.patch.object(desktop_package, "_select_host", return_value=("windows", "amd64")), \
                mock.patch.object(desktop_package.subprocess, "run", side_effect=fail_probe) as run:
            with redirect_stdout(output_capture), redirect_stderr(error_capture):
                with self.assertRaisesRegex(desktop_package.DesktopPlatformError, "command exited 1"):
                    desktop_package.main(["preflight-windows-temp"])

        self.assertEqual(output_capture.buffer.getvalue(), stdout)
        self.assertEqual(error_capture.buffer.getvalue(), stderr)
        self.assertEqual(run.call_args.args[0][0], sys.executable)
        self.assertEqual(run.call_args.kwargs["timeout"], desktop_package.WINDOWS_TEMP_PREFLIGHT_TIMEOUT_SECONDS)
        self.assertIsNone(run.call_args.kwargs["stdout"])
        self.assertIsNone(run.call_args.kwargs["stderr"])


if __name__ == "__main__":
    unittest.main()
