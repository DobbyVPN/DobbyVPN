from __future__ import annotations

import base64
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import torturer_checks.hosted.windows as hosted_windows
from torturer_checks.hosted.cli import CommandResult, HostedAdapterError
from torturer_checks.windows_job import WindowsJobCleanup
from torturer_contract.functional.engine import ScenarioExecutionError


def _decode_safe_powershell(command: tuple[str, ...]) -> tuple[str, tuple[str, ...]]:
    """Decode the test seam's fixed wrapper and its exact string arguments."""

    command_index = command.index("-Command")
    if command_index != 4 or len(command) != command_index + 2:
        raise AssertionError(command)
    payloads = re.findall(
        r"FromBase64String\('([A-Za-z0-9+/=]*)'\)", command[command_index + 1]
    )
    if not payloads:
        raise AssertionError(command)
    decoded = tuple(
        base64.b64decode(payload, validate=True).decode("utf-8")
        for payload in payloads
    )
    return decoded[0], decoded[1:]


class _FakeProcess:
    pid = 456

    def __init__(self) -> None:
        self.returncode: int | None = None
        self.wait_calls: list[float] = []
        self.kill_calls = 0
        self._torturer_windows_job = object()

    def wait(self, *, timeout: float | None = None) -> int:
        self.wait_calls.append(0.0 if timeout is None else timeout)
        if self.returncode is None:
            self.returncode = 0
        return self.returncode

    def poll(self) -> int | None:
        return self.returncode

    def kill(self) -> None:
        self.kill_calls += 1
        self.returncode = -9


class _ServiceRunner:
    def __init__(self, binary: Path, raw_directory: Path) -> None:
        self.binary = binary
        self.raw_directory = raw_directory
        self.service_alive = True
        self.ready = True
        self.tree_has_descendant = False
        self.tree_late_descendant = False
        self.calls: list[tuple[str, ...]] = []
        self.launch_kwargs: dict[str, object] | None = None

    def run(self, command, *, timeout_seconds: float) -> CommandResult:
        argv = tuple(command)
        self.calls.append(argv)
        if argv[0] != "powershell.exe":
            raise AssertionError(argv)
        script, arguments = _decode_safe_powershell(argv)
        if script == hosted_windows._WINDOWS_PROCESS_IDENTITY_SCRIPT:
            if not self.service_alive:
                return CommandResult(argv, 1, b"", b"")
            pid = int(arguments[0])
            return CommandResult(
                argv,
                0,
                f"service_identity={pid}|100\nservice_path={self.binary.resolve()}\n".encode(),
                b"",
            )
        if script == hosted_windows._WINDOWS_PROCESS_ALIVE_SCRIPT:
            pid = arguments[0]
            return CommandResult(
                argv,
                0 if self.service_alive else 2,
                (
                    f"service_probe_pid={pid}\n"
                    if self.service_alive
                    else "service_probe_absent\n"
                ).encode(),
                b"",
            )
        if script == hosted_windows._WINDOWS_PORT_READY_SCRIPT:
            return CommandResult(argv, 0 if self.ready else 1, b"True\n", b"")
        if script == hosted_windows._WINDOWS_PROCESS_TREE_SNAPSHOT_SCRIPT:
            rows = "tree_pid=123\ntree_identity=123|100\n"
            if self.tree_has_descendant:
                rows += "tree_pid=789\ntree_identity=789|200\n"
            return CommandResult(argv, 0, rows.encode(), b"")
        if script == hosted_windows._WINDOWS_PROCESS_TREE_VERIFY_SCRIPT:
            if self.tree_has_descendant:
                return CommandResult(argv, 1, b"survivor_pid=789\n", b"")
            self.service_alive = False
            return CommandResult(argv, 0, b"", b"")
        if script == hosted_windows._WINDOWS_EXTERNAL_PROCESS_STOP_SCRIPT:
            self.service_alive = False
            return CommandResult(argv, 0, b"external_service_stop=123\n", b"")
        if script == hosted_windows._WINDOWS_EXTERNAL_DESCENDANT_SNAPSHOT_SCRIPT:
            output = (
                b"late_tree_pid=789\nlate_tree_identity=789|200\n"
                if self.tree_late_descendant
                else b""
            )
            return CommandResult(argv, 0, output, b"")
        raise AssertionError(argv)


class HostedWindowsServiceJobTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory(prefix="windows-service-job-")
        root = Path(self.directory.name)
        self.binary = root / "service.exe"
        self.binary.write_bytes(b"synthetic executable")
        self.binary.chmod(0o700)
        self.scratch = root / "scratch"
        self.scratch.mkdir(mode=0o700)
        self.runner = _ServiceRunner(self.binary, self.scratch)
        self.controller = hosted_windows.WindowsServiceProcessController(
            pid=123,
            binary=self.binary,
            pid_file=root / "service.pid",
            runner=self.runner,
            raw_directory=self.scratch,
            control_address="127.0.0.1:50051",
        )
        self.process: _FakeProcess | None = None
        self.popen = mock.patch.object(
            hosted_windows,
            "popen_with_windows_job",
            side_effect=self._popen,
        )
        self.job_for = mock.patch.object(
            hosted_windows,
            "windows_job_for",
            side_effect=lambda process: getattr(process, "_torturer_windows_job", None),
        )
        self.terminate_job = mock.patch.object(
            hosted_windows,
            "terminate_windows_job",
            side_effect=self._terminate_job,
        )
        self.close_job = mock.patch.object(
            hosted_windows,
            "close_windows_job",
            side_effect=self._close_job,
        )
        self.popen.start()
        self.job_for.start()
        self.terminate_job.start()
        self.close_job.start()
        self.addCleanup(self.popen.stop)
        self.addCleanup(self.job_for.stop)
        self.addCleanup(self.terminate_job.stop)
        self.addCleanup(self.close_job.stop)
        self.addCleanup(self.directory.cleanup)
        self.close_diagnostics: list[tuple[str, ...]] = []
        self.cleanup_order: list[str] = []

    def test_controller_source_has_no_detached_launcher_or_recursive_kill_path(self) -> None:
        source = Path(hosted_windows.__file__).read_text(encoding="utf-8")
        self.assertNotIn("run_detached", source)
        self.assertNotIn("taskkill", source.lower())

    def test_powershell_wrapper_binds_exact_literal_arguments(self) -> None:
        arguments = (
            "123|456",
            r'C:\Program Files\Dobby "quoted"\service.exe',
            "$(Get-Process); exit 99",
            "' ; Get-Process | Stop-Process; '",
            "line one\nline two",
        )
        scripts = (
            hosted_windows._WINDOWS_PROCESS_ALIVE_SCRIPT,
            hosted_windows._WINDOWS_PROCESS_IDENTITY_SCRIPT,
            hosted_windows._WINDOWS_EXTERNAL_PROCESS_STOP_SCRIPT,
            hosted_windows._WINDOWS_EXTERNAL_DESCENDANT_SNAPSHOT_SCRIPT,
            hosted_windows._WINDOWS_PROCESS_PATH_SCRIPT,
            hosted_windows._WINDOWS_PROCESS_TREE_SNAPSHOT_SCRIPT,
            hosted_windows._WINDOWS_PROCESS_TREE_VERIFY_SCRIPT,
            hosted_windows._WINDOWS_PORT_READY_SCRIPT,
        )
        for script in scripts:
            with self.subTest(script=script):
                command = hosted_windows.WindowsServiceProcessController._powershell(
                    script, *arguments
                )
                decoded_script, decoded_arguments = _decode_safe_powershell(command)
                self.assertEqual(decoded_script, script)
                self.assertEqual(decoded_arguments, arguments)
                self.assertEqual(len(command), 6)
                wrapper = command[5]
                for argument in arguments:
                    self.assertNotIn(argument, wrapper)
                self.assertIn("FromBase64String", wrapper)

    def test_recorded_start_identity_mismatch_fails_before_any_stop(self) -> None:
        self.runner.calls.clear()
        with self.assertRaisesRegex(HostedAdapterError, "SERVICE_PID_NOT_CANDIDATE"):
            hosted_windows.WindowsServiceProcessController(
                pid=123,
                binary=self.binary,
                pid_file=Path(self.directory.name) / "mismatch.pid",
                runner=self.runner,
                raw_directory=self.scratch,
                control_address="127.0.0.1:50051",
                expected_initial_identity="123|999",
            )
        scripts = [_decode_safe_powershell(call)[0] for call in self.runner.calls]
        self.assertEqual(scripts, [hosted_windows._WINDOWS_PROCESS_IDENTITY_SCRIPT])
        self.assertNotIn(hosted_windows._WINDOWS_EXTERNAL_PROCESS_STOP_SCRIPT, scripts)

    def _popen(self, popen, command, **kwargs):
        self.assertIs(popen, subprocess.Popen)
        self.assertEqual(command, [str(self.binary), "-port", "50051"])
        self.assertIs(kwargs["stdout"], subprocess.DEVNULL)
        self.assertIs(kwargs["stderr"], subprocess.DEVNULL)
        self.runner.launch_kwargs = kwargs
        process = _FakeProcess()
        self.process = process
        return process

    def _terminate_job(self, process, *, deadline: float, stage: str):
        self.cleanup_order.append("terminate-job")
        process.returncode = 0
        self.runner.service_alive = False
        return WindowsJobCleanup(True, 0, ("api=QueryInformationJobObject winerror=0",))

    def _close_job(self, process, *, stage: str, deadline: float):
        self.cleanup_order.append("close-job")
        diagnostics = self.close_diagnostics.pop(0) if self.close_diagnostics else ()
        if not diagnostics:
            delattr(process, "_torturer_windows_job")
        return diagnostics

    def test_replacement_is_direct_popen_with_discarded_output(self) -> None:
        self.controller._start(2.0)
        self.assertIsNotNone(self.process)
        assert self.process is not None
        self.assertEqual(self.runner.launch_kwargs["stdin"], subprocess.DEVNULL)
        self.assertEqual(self.runner.launch_kwargs["stdout"], subprocess.DEVNULL)
        self.assertEqual(self.runner.launch_kwargs["stderr"], subprocess.DEVNULL)
        self.assertFalse(any(call[0] == "powershell.exe" and "Start-Process" in call for call in self.runner.calls))

    def test_stop_orders_job_proof_reap_then_close(self) -> None:
        self.controller._start(2.0)
        self.controller._terminate(2.0)
        self.assertEqual(self.cleanup_order, ["terminate-job", "close-job"])
        assert self.process is not None
        self.assertEqual(len(self.process.wait_calls), 1)
        self.assertIsNone(getattr(self.process, "_torturer_windows_job", None))
        self.assertEqual(
            self.controller._service_diagnostics,
            ["api=QueryInformationJobObject winerror=0"],
        )

    def test_identity_failure_immediately_attempts_owned_job_cleanup(self) -> None:
        # Fail before readiness is checked, after direct Popen has returned.
        with mock.patch.object(
            self.controller,
            "_verify_candidate_pid",
            side_effect=ScenarioExecutionError("SERVICE_PID_NOT_CANDIDATE"),
        ):
            with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_PID_NOT_CANDIDATE"):
                self.controller._start(2.0)
        # Its process was terminated in _start's catch path before the
        # original error escaped.
        self.assertIsNotNone(self.process)
        self.assertIn("terminate-job", self.cleanup_order)

    def test_readiness_failure_immediately_cleans_the_job(self) -> None:
        self.runner.ready = False
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_RESTART_TIMEOUT|SERVICE_RESTART_NOT_READY"):
            self.controller._start(0.05)
        self.assertIn("terminate-job", self.cleanup_order)
        self.assertIsNotNone(self.process)

    def test_close_error_is_fail_closed_and_retried_with_ownership_retained(self) -> None:
        self.controller._start(2.0)
        self.close_diagnostics.append(("api=CloseHandle winerror=6",))
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_JOB_CLOSE_FAILED"):
            self.controller._terminate(2.0)
        assert self.process is not None
        self.assertIsNotNone(getattr(self.process, "_torturer_windows_job", None))
        self.assertEqual(
            self.controller._service_diagnostics,
            [
                "api=QueryInformationJobObject winerror=0",
                "api=CloseHandle winerror=6",
                "api=CloseHandle winerror=6 detail=replacement-job-still-attached",
            ],
        )
        self.controller._terminate(2.0)
        self.assertIsNone(getattr(self.process, "_torturer_windows_job", None))

    def test_persistent_close_error_fails_closed_without_losing_job(self) -> None:
        self.controller._start(2.0)
        self.close_diagnostics.append(("api=CloseHandle winerror=6",))
        self.close_diagnostics.append(("api=CloseHandle winerror=6",))
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_JOB_CLOSE_FAILED"):
            self.controller._terminate(2.0)
        assert self.process is not None
        self.assertIsNotNone(getattr(self.process, "_torturer_windows_job", None))

    def test_initial_external_descendant_is_hard_failure_without_job_claim(self) -> None:
        self.runner.tree_has_descendant = True
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_TREE_SURVIVED"):
            self.controller._terminate(2.0)
        self.assertFalse(self.controller._external_tree_cleanup_proven)
        self.assertNotIn("terminate-job", self.cleanup_order)
        self.assertFalse(any("taskkill" in " ".join(call).lower() for call in self.runner.calls))

    def test_late_external_descendant_is_found_by_fresh_parent_census(self) -> None:
        self.runner.tree_late_descendant = True
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_TREE_SURVIVED"):
            self.controller._terminate(2.0)
        self.assertFalse(self.controller._external_tree_cleanup_proven)
        self.assertTrue(
            any(
                call[0] == "powershell.exe"
                and _decode_safe_powershell(call)[0]
                == hosted_windows._WINDOWS_EXTERNAL_DESCENDANT_SNAPSHOT_SCRIPT
                for call in self.runner.calls
            )
        )


if __name__ == "__main__":
    unittest.main()
