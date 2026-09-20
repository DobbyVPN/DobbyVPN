from __future__ import annotations

import base64
import os
import re
import time

from pathlib import Path
import tempfile
import unittest

from unittest import mock
import torturer_checks.hosted.windows as hosted_windows
from torturer_checks.hosted.cli import CommandResult, HostedAdapterError
from torturer_checks.hosted.factory import adapter_for_platform
from torturer_checks.hosted.macos import (
    MacOSHostedAdapter,
    _MACOS_LAUNCHD_KILL,
    _MACOS_LAUNCHD_LABEL,
    _default_control_socket,
    _parse_macos_launchd_job,
)
from torturer_checks.hosted.windows import (
    WindowsHostedAdapter,
    WindowsServiceProcessController,
    _WINDOWS_PORT_READY_SCRIPT,
    _WINDOWS_PROCESS_ALIVE_SCRIPT,
    _WINDOWS_PROCESS_IDENTITY_SCRIPT,
    _WINDOWS_PROCESS_TREE_SNAPSHOT_SCRIPT,
    _WINDOWS_PROCESS_TREE_VERIFY_SCRIPT,
    _parse_windows_process_identity,
    _parse_windows_tree_snapshot,
)
from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.engine import FunctionalEngine, ScenarioExecutionError
from torturer_checks.windows_job import WindowsJobCleanup


def _decode_safe_powershell(command: tuple[str, ...]) -> tuple[str, tuple[str, ...]]:
    """Decode the fixed PowerShell wrapper and its exact string arguments."""

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


class _FakeWindowsReplacementProcess:
    def __init__(self) -> None:
        self.pid = 456
        self.returncode: int | None = None
        self.wait_calls: list[float] = []
        self._torturer_windows_job = object()

    def wait(self, *, timeout: float | None = None) -> int:
        self.wait_calls.append(0.0 if timeout is None else timeout)
        if self.returncode is None:
            self.returncode = 0
        return self.returncode

    def poll(self) -> int | None:
        return self.returncode
from torturer_contract.functional.scenarios import get_scenario


class HostedDesktopProcessRunner:
    """Deterministic command seam for the two hosted process controllers."""

    def __init__(self, *, platform: str, binary: Path) -> None:
        self.platform = platform
        self.binary = binary
        self.calls: list[tuple[str, ...]] = []
        self.timeouts: list[float] = []
        self.raw_directory: Path | None = None
        self.service_alive = True
        self.mac_child_alive = True
        self.service_pid = 123
        self.service_runs = 1
        self.kill_result: CommandResult | None = None
        self.identity_reused = False
        self.probe_error = False
        self.tree_survivor = False
        self.tree_identity_mismatch = False
        self.tree_partial = False
        self.tree_late_descendant = False
        self.mac_descendant_binary: Path | None = None
        self.connected = False
        self.external_calls = 0

    def run(self, command, *, timeout_seconds):
        argv = tuple(command)
        self.calls.append(argv)
        self.timeouts.append(float(timeout_seconds))

        if argv[:2] == ("sudo", "-n"):
            return self.run(argv[2:], timeout_seconds=timeout_seconds)

        if argv[0] == "taskkill.exe":
            self.service_alive = False
            return CommandResult(argv, 0, b"terminated\n", b"")
        if argv[0] == "launchctl":
            if argv[1] == "kill":
                if self.kill_result is not None:
                    return self.kill_result
                self.service_alive = False
                self.mac_child_alive = False
                return CommandResult(argv, 0, b"", b"")
            if argv[1] == "print":
                if self.probe_error:
                    return CommandResult(argv, 1, b"", b"permission denied\n")
                if not self.service_alive:
                    # Model launchd KeepAlive: the replacement is discovered
                    # by the next print, never started by the controller.
                    self.service_alive = True
                    self.mac_child_alive = True
                    self.service_pid = 456
                    self.service_runs += 1
                return CommandResult(
                    argv,
                    0,
                    (
                        f"{_MACOS_LAUNCHD_LABEL} = {{\n"
                        f"\tprogram = {self.binary.resolve()}\n"
                        f"\tpid = {self.service_pid}\n"
                        f"\truns = {self.service_runs}\n"
                        "}\n"
                    ).encode(),
                    b"",
                )
        if argv[0] == "kill":
            if argv[1] == "-KILL":
                if self.platform == "macos" and len(argv) > 2:
                    if argv[2].startswith("-") or argv[2] == str(self.service_pid):
                        self.service_alive = False
                        self.mac_child_alive = False
                    else:
                        self.mac_child_alive = False
                else:
                    self.service_alive = False
                return CommandResult(argv, 0, b"", b"")
            if argv[1] == "-0":
                return CommandResult(argv, 0 if self.service_alive else 1, b"", b"")
        if argv[0] == "sh" and argv[1] == "-c":
            self.service_alive = True
            self.mac_child_alive = True
            self.service_pid = 456
            return CommandResult(argv, 0, b"456\n", b"")
        if argv[0] == "python3" and len(argv) > 2 and "proc_pidinfo" in argv[2]:
            identity_pid = int(argv[3])
            ticks = "2000.000001" if self.identity_reused else "1000.000001"
            alive = self.service_alive if identity_pid == self.service_pid else self.mac_child_alive
            identity_path = self.binary.resolve()
            if identity_pid != self.service_pid and self.mac_descendant_binary is not None:
                identity_path = self.mac_descendant_binary.resolve()
            return CommandResult(
                argv,
                0 if alive else 2,
                f"service_identity={identity_pid}|{ticks}\nservice_path={identity_path}\n".encode()
                if alive else b"service_probe_absent\n",
                b"" if alive else b"",
            )
        if argv[0] == "python3" and len(argv) > 2 and "service_probe_pid" in argv[2]:
            if self.probe_error:
                return CommandResult(argv, 1, b"service_probe_error\n", b"")
            return CommandResult(
                argv,
                0 if self.service_alive else 2,
                (
                    f"service_probe_pid={self.service_pid}\n"
                    if self.service_alive
                    else "service_probe_absent\n"
                ).encode(),
                b"",
            )
        if argv[0] == "python3":
            return CommandResult(argv, 0 if self.service_alive else 1, b"", b"")
        if argv[0] == "ps":
            if argv[1:3] == ("-axo", "pid=,ppid=,pgid=,state="):
                if not self.service_alive and not (self.platform == "macos" and self.mac_child_alive):
                    return CommandResult(argv, 0, b"", b"")
                if self.platform == "macos" and not self.service_alive:
                    return CommandResult(
                        argv,
                        0,
                        b"456 123 123 R\n",
                        b"",
                    )
                if self.platform == "macos":
                    child_pid = "456" if self.service_pid == 123 else "789"
                    child_parent = str(self.service_pid)
                    return CommandResult(
                        argv,
                        0,
                        (
                            f"{self.service_pid} 1 {self.service_pid} R\n"
                            f"{child_pid} {child_parent} {self.service_pid} R\n"
                        ).encode(),
                        b"",
                    )
                return CommandResult(
                    argv,
                    0,
                    b"123 1 123 R\n456 123 123 R\n",
                    b"",
                )
            if argv[1:3] == ("-p", str(self.service_pid)):
                if self.probe_error:
                    return CommandResult(argv, 1, b"", b"permission denied\n")
                if not self.service_alive:
                    return CommandResult(argv, 1, b"", b"")
                if argv[-1] == "pid=":
                    return CommandResult(argv, 0, f"{self.service_pid}\n".encode(), b"")
                start = "Mon Aug 23 10:00:01 2026" if self.identity_reused else "Mon Aug 23 10:00:00 2026"
                return CommandResult(
                    argv,
                    0,
                    f"{self.service_pid} {start} {self.binary.resolve()}\n".encode(),
                    b"",
                )
            return CommandResult(argv, 0, (str(self.binary.resolve()) + "\n").encode(), b"")
        if argv[0] == "powershell.exe":
            script, arguments = _decode_safe_powershell(argv)
            if script == hosted_windows._WINDOWS_EXTERNAL_DESCENDANT_SNAPSHOT_SCRIPT:
                if arguments != (str(self.service_pid),):
                    raise AssertionError(arguments)
                output = (
                    b"late_tree_pid=789\nlate_tree_identity=789|200\n"
                    if self.tree_late_descendant
                    else b""
                )
                return CommandResult(argv, 0, output, b"")
            if "tree_pid=" in script:
                if arguments != (str(self.service_pid),):
                    raise AssertionError(arguments)
                if self.tree_partial:
                    return CommandResult(argv, 0, b"tree_pid=123\nmalformed-tree-row\n", b"")
                return CommandResult(
                    argv,
                    0,
                    (
                        f"tree_pid={self.service_pid}\n"
                        f"tree_identity={self.service_pid}|100\n"
                    ).encode(),
                    b"",
                )
            if "survivor_pid=" in script:
                expected_tree = (f"{self.service_pid}|100",)
                if arguments != expected_tree:
                    raise AssertionError(arguments)
                if self.tree_identity_mismatch:
                    return CommandResult(
                        argv,
                        0,
                        b"identity_mismatch_pid=123\n"
                        b"identity_expected=123|100\n"
                        b"identity_observed=123|200\n",
                        b"",
                    )
                return CommandResult(
                    argv,
                    1 if self.tree_survivor else 0,
                    b"survivor_pid=789\n" if self.tree_survivor else b"",
                    b"tree diagnostic\n" if self.tree_survivor else b"",
                )
            if "Start-Process" in script:
                self.service_alive = True
                self.service_pid = 456
                if len(arguments) != 2:
                    raise AssertionError(arguments)
                Path(arguments[0]).write_bytes(b"windows service stdout\n")
                Path(arguments[1]).write_bytes(b"windows service stderr\n")
                Path(arguments[0]).chmod(0o600)
                Path(arguments[1]).chmod(0o600)
                return CommandResult(argv, 0, b"456\n", b"")
            if script == hosted_windows._WINDOWS_EXTERNAL_PROCESS_STOP_SCRIPT:
                if arguments != (
                    str(self.service_pid),
                    "100",
                    str(self.binary.resolve()),
                ):
                    raise AssertionError(arguments)
                self.service_alive = False
                return CommandResult(argv, 0, b"external_service_stop=123\n", b"")
            if script == _WINDOWS_PROCESS_IDENTITY_SCRIPT:
                if arguments != (str(self.service_pid),):
                    raise AssertionError(arguments)
                if not self.service_alive:
                    return CommandResult(argv, 1, b"", b"")
                ticks = 200 if self.identity_reused else 100
                return CommandResult(
                    argv,
                    0,
                    f"service_identity={self.service_pid}|{ticks}\nservice_path={self.binary.resolve()}\n".encode(),
                    b"",
                )
            if "Get-CimInstance" in script:
                return CommandResult(
                    argv, 0 if self.service_alive else 1,
                    (str(self.binary.resolve()) + "\n").encode(), b"",
                )
            if "Get-Process" in script:
                if arguments != (str(self.service_pid),):
                    raise AssertionError(arguments)
                if self.probe_error:
                    return CommandResult(argv, 1, b"service_probe_error\n", b"")
                return CommandResult(
                    argv,
                    0 if self.service_alive else 2,
                    (
                        f"service_probe_pid={self.service_pid}\n"
                        if self.service_alive
                        else "service_probe_absent\n"
                    ).encode(),
                    b"",
                )
            if "Test-NetConnection" in script:
                if arguments != ("127.0.0.1", "50051"):
                    raise AssertionError(arguments)
                return CommandResult(argv, 0 if self.service_alive else 1, b"True\n", b"")

        if argv[0] == "curl":
            self.external_calls += 1
            value = (
                b"198.51.100.10\n"
                if self.external_calls == 1 or not self.connected
                else b"203.0.113.10\n"
            )
            return CommandResult(argv, 0, value, b"")
        if argv[0] == str(self.binary):
            operation = argv[1]
            if operation == "profile-inventory":
                return CommandResult(
                    argv, 0, b'{"profiles":[{"index":0,"protocol":"OUTLINE"}]}\n', b""
                )
            if operation == "check-config":
                return CommandResult(argv, 0, b"profiles=1 source=file\n", b"")
            if operation == "connect-profile":
                self.connected = True
                return CommandResult(argv, 0, b"CONNECTED\n", b"")
            if operation == "status":
                state = b"Connected" if self.connected else b"Disconnected"
                code = b"2" if self.connected else b"0"
                return CommandResult(argv, 0, b'{"code":' + code + b',"state":"' + state + b'"}\n', b"")
            if operation == "external-ip":
                self.external_calls += 1
                value = (
                    b"198.51.100.10\n"
                    if self.external_calls == 1 or not self.connected
                    else b"203.0.113.10\n"
                )
                return CommandResult(argv, 0, value, b"")
            if operation == "disconnect":
                self.connected = False
                return CommandResult(argv, 0, b"DISCONNECTED\n", b"")
        raise AssertionError(argv)


class HostedDesktopAdapterTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory(prefix="hosted-desktop-adapter-")
        root = Path(self.directory.name)
        self.cli = root / "dobby-cli"
        self.cli.write_bytes(b"synthetic executable")
        self.cli.chmod(0o700)
        self.profile = root / "profile.toml"
        self.profile.write_text("[[Outline]]\nPassword = \"synthetic\"\n", encoding="utf-8")
        self.profile.chmod(0o600)
        self._current_windows_runner: HostedDesktopProcessRunner | None = None
        self.windows_launches: list[tuple[object, tuple[str, ...], dict[str, object]]] = []
        self._windows_popen_patch = mock.patch.object(
            hosted_windows,
            "popen_with_windows_job",
            side_effect=self._popen_windows_service,
        )
        self._windows_job_patch = mock.patch.object(
            hosted_windows,
            "windows_job_for",
            side_effect=lambda process: getattr(process, "_torturer_windows_job", None),
        )
        self._windows_terminate_patch = mock.patch.object(
            hosted_windows,
            "terminate_windows_job",
            side_effect=self._terminate_windows_service,
        )
        self._windows_close_patch = mock.patch.object(
            hosted_windows,
            "close_windows_job",
            side_effect=self._close_windows_service,
        )
        self._windows_popen_patch.start()
        self._windows_job_patch.start()
        self._windows_terminate_patch.start()
        self._windows_close_patch.start()
        self.addCleanup(self._windows_popen_patch.stop)
        self.addCleanup(self._windows_job_patch.stop)
        self.addCleanup(self._windows_terminate_patch.stop)
        self.addCleanup(self._windows_close_patch.stop)
        self.addCleanup(self.directory.cleanup)

    def _popen_windows_service(self, popen, command, **kwargs):
        self.assertIs(popen, hosted_windows.subprocess.Popen)
        runner = self._current_windows_runner
        self.assertIsNotNone(runner)
        assert runner is not None
        process = _FakeWindowsReplacementProcess()
        self.windows_launches.append((popen, tuple(command), kwargs))
        self.assertIs(kwargs["stdout"], hosted_windows.subprocess.PIPE)
        self.assertIs(kwargs["stderr"], hosted_windows.subprocess.PIPE)
        runner.service_alive = True
        runner.service_pid = process.pid
        return process

    def _terminate_windows_service(self, process, *, deadline: float, stage: str):
        runner = self._current_windows_runner
        assert runner is not None
        process.returncode = 0
        runner.service_alive = False
        return WindowsJobCleanup(True, 0, ())

    def _close_windows_service(self, process, *, deadline: float | None, stage: str):
        delattr(process, "_torturer_windows_job")
        return ()

    def _provenance(self, adapter, platform: str):
        from torturer_contract.functional.results import RunProvenance

        return RunProvenance(
            platform=platform,
            platform_version="24.04",
            architecture="amd64",
        )

    def _adapter(
        self,
        platform: str,
        runner: HostedDesktopProcessRunner,
        *,
        identity_file: Path | None = None,
    ):
        if platform == "windows":
            self._current_windows_runner = runner
        return adapter_for_platform(
            platform,
            cli=self.cli,
            profile=self.profile,
            runner=runner,
            service_pid=123,
            service_binary=self.cli,
            service_identity_file=identity_file,
            service_socket=(
                Path("127.0.0.1:50051")
                if platform == "windows"
                else Path(self.directory.name) / "control.sock"
            ),
        )

    def test_windows_and_macos_process_loss_are_canonical_and_bounded(self) -> None:
        scenario = get_scenario("functional.product-process-loss")
        for platform, adapter_class in (("windows", WindowsHostedAdapter), ("macos", MacOSHostedAdapter)):
            runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
            runner.raw_directory = Path(self.directory.name) / f"{platform}-raw"
            adapter = self._adapter(platform, runner)
            self.assertIsInstance(adapter, adapter_class)
            self.assertIsNone(adapter.service.pid_file)
            self.assertIn(Capability.PROCESS_LOSS, adapter.capabilities)
            connections = adapter.discover_connections()
            adapter.select_connection(connections[0])

            result = FunctionalEngine().run(
                scenario, adapter, self._provenance(adapter, platform), connections[0]
            )

            self.assertEqual(result.outcome, "passed")
            self.assertTrue(result.cleanup["verified"])
            if platform == "windows":
                self.assertFalse(any(call[0] == "taskkill.exe" for call in runner.calls))
                self.assertTrue(self.windows_launches)
                self.assertTrue(any(call[0] == "powershell.exe" for call in runner.calls))
            else:
                self.assertFalse(any(call[0] in {"kill", "sh"} for call in runner.calls))
                self.assertTrue(any(call[0] == "launchctl" for call in runner.calls))
            self.assertTrue(all(timeout <= 120 for timeout in runner.timeouts))

            if platform == "windows":
                self.assertTrue(any(
                    call[0] == "powershell.exe"
                    and "Test-NetConnection" in _decode_safe_powershell(call)[0]
                    for call in runner.calls
                ))
            else:
                self.assertIn(_MACOS_LAUNCHD_KILL, runner.calls)
                self.assertFalse(any(call[0] in {"sh", "kill"} for call in runner.calls))
                self.assertTrue(any(call[:2] == ("python3", "-c") for call in runner.calls))

    def test_native_ui_process_loss_restarts_service_without_cli_reconnect(self) -> None:
        for platform in ("windows", "macos"):
            runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
            runner.raw_directory = Path(self.directory.name) / f"{platform}-native-loss-raw"
            adapter = self._adapter(platform, runner)
            connections = adapter.discover_connections()
            adapter.select_connection(connections[0])
            runner.calls.clear()

            result = adapter.restart_service_for_native_ui(5.0)

            self.assertEqual(result, {"process_loss_verified": True})
            self.assertFalse(any(
                call[0] == str(self.cli) and "connect-profile" in call
                for call in runner.calls
            ))
            if platform == "windows":
                self.assertTrue(self.windows_launches)
            else:
                self.assertIn(_MACOS_LAUNCHD_KILL, runner.calls)

    def test_macos_launchd_parser_requires_program_pid_and_runs(self) -> None:
        job = _parse_macos_launchd_job(
            "program = /candidate\npid = 123\nruns = 2\n"
        )
        self.assertEqual(job.program, "/candidate")
        self.assertEqual(job.pid, 123)
        self.assertEqual(job.runs, 2)
        for value in (
            "pid = 123\nruns = 2\n",
            "program = /candidate\npid = 123\nruns = 0\n",
            "program = /candidate\npid = 123\nruns = 2\nruns = 3\n",
        ):
            with self.subTest(value=value), self.assertRaises(ValueError):
                _parse_macos_launchd_job(value)

    def test_macos_process_loss_uses_launchd_and_keeps_replacement_owned(self) -> None:
        runner = HostedDesktopProcessRunner(platform="macos", binary=self.cli)
        runner.raw_directory = Path(self.directory.name) / "macos-launchd-raw"
        adapter = self._adapter("macos", runner)
        assert adapter.service is not None
        adapter.service.restart_after_loss(5.0)
        adapter.finalize(5.0)
        self.assertIn(_MACOS_LAUNCHD_KILL, runner.calls)
        self.assertFalse(any(call[:2] == ("sudo", "-n") and call[2] in {"sh", "kill"} for call in runner.calls))
        self.assertFalse(any(call[0] == str(self.cli) for call in runner.calls))
        error_runner = HostedDesktopProcessRunner(platform="macos", binary=self.cli)
        error_runner.raw_directory = Path(self.directory.name) / "macos-kill-error-raw"
        error_adapter = self._adapter("macos", error_runner)
        assert error_adapter.service is not None
        error_runner.kill_result = CommandResult(
            _MACOS_LAUNCHD_KILL,
            1,
            b"kill stdout\n",
            b"kill stderr\n",
        )
        with self.assertRaisesRegex(
            ScenarioExecutionError, "SERVICE_KILL_FAILED"
        ) as raised:
            error_adapter.service._terminate(5.0)
        notes = "\n".join(getattr(raised.exception, "__notes__", ()))
        self.assertIn("command_stdout:\nkill stdout", notes)
        self.assertIn("command_stderr:\nkill stderr", notes)

    def test_windows_process_probes_preserve_diagnostics(self) -> None:
        self.assertNotIn("Out-Null", _WINDOWS_PROCESS_ALIVE_SCRIPT)
        self.assertIn("Write-Output", _WINDOWS_PROCESS_ALIVE_SCRIPT)
        self.assertNotIn("-InformationLevel Quiet", _WINDOWS_PORT_READY_SCRIPT)
        self.assertIn("Write-Output $ready", _WINDOWS_PORT_READY_SCRIPT)
        self.assertIn("$ready.TcpTestSucceeded", _WINDOWS_PORT_READY_SCRIPT)
        self.assertIn("tree_pid=", _WINDOWS_PROCESS_TREE_SNAPSHOT_SCRIPT)
        self.assertIn("CreationDate", _WINDOWS_PROCESS_TREE_SNAPSHOT_SCRIPT)
        self.assertIn("tree_identity=", _WINDOWS_PROCESS_TREE_SNAPSHOT_SCRIPT)
        self.assertIn("survivor_pid=", _WINDOWS_PROCESS_TREE_VERIFY_SCRIPT)
        self.assertIn("identity_mismatch_pid=", _WINDOWS_PROCESS_TREE_VERIFY_SCRIPT)
        self.assertNotIn("$pid =", _WINDOWS_PROCESS_TREE_SNAPSHOT_SCRIPT)

    def test_macos_process_loss_uses_launchd_generation_not_native_probe(self) -> None:
        from torturer_checks.hosted import macos as hosted_macos

        self.assertIn("launchctl", _MACOS_LAUNCHD_KILL)
        self.assertEqual(_MACOS_LAUNCHD_LABEL, "system/com.dobby.vpnservice")
        self.assertFalse(hasattr(hosted_macos, "_MACOS_PROCESS_IDENTITY"))
        self.assertFalse(hasattr(hosted_macos, "_MACOS_PROCESS_ALIVE"))

    def test_native_identity_parsers_ignore_extras_but_reject_ambiguous_identity(self) -> None:
        windows = "service_identity=123|100\nservice_path=C:\\candidate.exe\n"
        self.assertEqual(
            _parse_windows_process_identity(windows, 123),
            ("123|100", "C:\\candidate.exe"),
        )
        with self.assertRaises(ValueError):
            _parse_windows_process_identity(windows + "service_path=other.exe\n", 123)
        self.assertEqual(
            _parse_windows_process_identity(windows + "diagnostic=available\n", 123),
            ("123|100", "C:\\candidate.exe"),
        )


    def test_windows_cleanup_rejects_partial_tree_snapshot(self) -> None:
        identities, complete = _parse_windows_tree_snapshot(
            "tree_pid=123\nmalformed-tree-row\n"
        )
        self.assertEqual(identities, ())
        self.assertFalse(complete)
        runner = HostedDesktopProcessRunner(platform="windows", binary=self.cli)
        runner.raw_directory = Path(self.directory.name) / "windows-partial-tree-raw"
        runner.tree_partial = True
        adapter = self._adapter("windows", runner)
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_TREE_PROBE_FAILED"):
            adapter.service._terminate(5.0)
        self.assertFalse(any(call[0] == "taskkill.exe" for call in runner.calls))

    def test_windows_service_process_reuse_is_not_a_survivor(self) -> None:
        runner = HostedDesktopProcessRunner(platform="windows", binary=self.cli)
        runner.raw_directory = Path(self.directory.name) / "windows-reused-pid-raw"
        runner.tree_identity_mismatch = True
        adapter = self._adapter("windows", runner)
        adapter.service._terminate(5.0)
        self.assertTrue(adapter.service._external_tree_cleanup_proven)
        self.assertTrue(any(call[0] == "powershell.exe" for call in runner.calls))

    def test_windows_service_restart_does_not_create_raw_stream_files(self) -> None:
        runner = HostedDesktopProcessRunner(platform="windows", binary=self.cli)
        raw = Path(self.directory.name) / "windows-no-raw"
        raw.mkdir(mode=0o700)
        runner.raw_directory = raw
        adapter = self._adapter("windows", runner)
        adapter.service._start(10.0)
        self.assertIs(self.windows_launches[-1][2]["stdout"], hosted_windows.subprocess.PIPE)
        self.assertIs(self.windows_launches[-1][2]["stderr"], hosted_windows.subprocess.PIPE)
        self.assertEqual(list(raw.glob("*.raw.log")), [])
        adapter.service._terminate(5.0)

    def test_windows_restart_deadline_has_no_stream_finalization_stage(self) -> None:
        runner = HostedDesktopProcessRunner(platform="windows", binary=self.cli)
        runner.raw_directory = Path(self.directory.name) / "windows-deadline-no-raw"
        adapter = self._adapter("windows", runner)
        service = adapter.service
        self.assertIsNotNone(service)
        assert service is not None
        service._start(5.0)
        self.assertEqual(list(runner.raw_directory.glob("*.raw.log")), [])

    def test_windows_service_process_loss_rejects_surviving_descendant(self) -> None:
        runner = HostedDesktopProcessRunner(platform="windows", binary=self.cli)
        runner.raw_directory = Path(self.directory.name) / "windows-survivor-raw"
        adapter = self._adapter("windows", runner)
        runner.tree_survivor = True
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_TREE_SURVIVED"):
            adapter.service._terminate(5.0)

    def test_factory_wires_each_desktop_process_seam_without_changing_linux(self) -> None:
        for platform in ("windows", "macos"):
            runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
            runner.raw_directory = Path(self.directory.name) / f"{platform}-factory-raw"
            adapter = self._adapter(platform, runner)
            self.assertIn(Capability.PROCESS_LOSS, adapter.capabilities)

    def test_adapter_finalization_stops_only_the_restarted_desktop_service(self) -> None:
        for platform in ("windows", "macos"):
            with self.subTest(platform=platform):
                runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
                raw = Path(self.directory.name) / f"{platform}-finalize-raw"
                raw.mkdir(mode=0o700)
                runner.raw_directory = raw
                adapter = self._adapter(platform, runner)
                service = adapter.service
                self.assertIsNotNone(service)
                assert service is not None

                # The initial service remains host-owned.  Only a process-loss
                # restart opts the controller into adapter-owned finalization.
                adapter.finalize(5.0)
                self.assertFalse(any(
                    call[0] in {"taskkill.exe", "kill"} for call in runner.calls
                ))
                if platform == "windows":
                    service._start(5.0)
                else:
                    service._restart_number = 1
                    service._replacement_job = _parse_macos_launchd_job(
                        f"program = {self.cli.resolve()}\npid = 123\nruns = 1\n"
                    )

                adapter.finalize(5.0)
                if platform == "windows":
                    self.assertFalse(any(call[0] == "taskkill.exe" for call in runner.calls))
                else:
                    self.assertFalse(any(call[0] == "kill" for call in runner.calls))

    def test_desktop_finalization_refuses_same_path_reused_root_pid(self) -> None:
        for platform in ("windows",):
            with self.subTest(platform=platform):
                runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
                raw = Path(self.directory.name) / f"{platform}-reused-root-raw"
                raw.mkdir(mode=0o700)
                runner.raw_directory = raw
                adapter = self._adapter(platform, runner)
                assert adapter.service is not None
                service = adapter.service
                service._restart_number = 1
                service._replacement_identity = "123|100"
                runner.identity_reused = True
                with self.assertRaisesRegex(
                    (ScenarioExecutionError, HostedAdapterError),
                    "SERVICE_PID_NOT_CANDIDATE",
                ):
                    adapter.finalize(5.0)
                self.assertFalse(any(
                    call[0] in {"taskkill.exe", "kill"} for call in runner.calls
                ))

    def test_desktop_finalization_refuses_an_unowned_partial_restart(self) -> None:
        for platform in ("windows", "macos"):
            with self.subTest(platform=platform):
                runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
                runner.raw_directory = Path(self.directory.name) / f"{platform}-partial-raw"
                adapter = self._adapter(platform, runner)
                assert adapter.service is not None
                adapter.service._restart_number = 1
                with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_PID_PROBE_FAILED"):
                    adapter.finalize(5.0)
                self.assertFalse(any(
                    call[0] in {"taskkill.exe", "kill"} for call in runner.calls
                ))

    def test_desktop_process_loss_refuses_a_reused_initial_pid(self) -> None:
        for platform in ("windows",):
            with self.subTest(platform=platform):
                runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
                runner.raw_directory = Path(self.directory.name) / f"{platform}-initial-reuse-raw"
                adapter = self._adapter(platform, runner)
                assert adapter.service is not None
                runner.identity_reused = True
                with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_PID_NOT_CANDIDATE"):
                    adapter.service.restart_after_loss(5.0)
                self.assertFalse(any(
                    call[0] in {"taskkill.exe", "kill"} for call in runner.calls
                ))

    def test_desktop_liveness_probe_error_is_not_absence(self) -> None:
        for platform in ("windows", "macos"):
            with self.subTest(platform=platform):
                runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
                raw = Path(self.directory.name) / f"{platform}-probe-error-raw"
                raw.mkdir(mode=0o700)
                runner.raw_directory = raw
                adapter = self._adapter(platform, runner)
                assert adapter.service is not None
                runner.probe_error = True
                expected_error = (
                    "SERVICE_PROBE_FAILED"
                    if platform == "windows"
                    else "SERVICE_LAUNCHD_PROBE_FAILED"
                )
                with self.assertRaisesRegex(ScenarioExecutionError, expected_error):
                    adapter.service._alive(1.0)

    def test_windows_absence_marker_with_stderr_is_probe_failure(self) -> None:
        controller = object.__new__(WindowsServiceProcessController)
        controller.pid = 123
        controller._probe = mock.Mock(
            return_value=CommandResult(
                ("powershell.exe",), 2, b"service_probe_absent\n", b"warning\n"
            )
        )
        with self.assertRaisesRegex(ScenarioExecutionError, "SERVICE_PROBE_FAILED"):
            controller._alive(1.0)

    def test_desktop_restart_uses_detached_launcher_when_runner_provides_one(self) -> None:
        class DetachedRunner(HostedDesktopProcessRunner):
            def __init__(self, platform: str, binary: Path) -> None:
                super().__init__(platform=platform, binary=binary)
                self.detached_calls: list[tuple[str, ...]] = []

            def run_detached(self, command, *, timeout_seconds):
                self.detached_calls.append(tuple(command))
                return self.run(command, timeout_seconds=timeout_seconds)

        for platform in ("windows", "macos"):
            with self.subTest(platform=platform):
                runner = DetachedRunner(platform, self.cli)
                runner.raw_directory = Path(self.directory.name) / f"{platform}-detached-raw"
                runner.raw_directory.mkdir(mode=0o700)
                adapter = self._adapter(platform, runner)
                assert adapter.service is not None
                if platform == "macos":
                    # launchd must observe the service loss before it exposes
                    # the KeepAlive replacement to the controller.
                    adapter.service._terminate(5.0)
                    adapter.service._wait_dead(time.monotonic() + 5.0)
                adapter.service._start(5.0)
                if platform == "macos":
                    self.assertEqual(runner.detached_calls, [])
                    self.assertTrue(any(call[0] == "launchctl" for call in runner.calls))
                else:
                    self.assertEqual(len(self.windows_launches), 1)
                    self.assertEqual(
                        self.windows_launches[0][1],
                        (str(self.cli), "-port", "50051"),
                    )

    def test_pid_file_write_failure_retains_owned_identity_for_finalization(self) -> None:
        for platform in ("windows",):
            with self.subTest(platform=platform):
                runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
                raw = Path(self.directory.name) / f"{platform}-pid-write-failure-raw"
                raw.mkdir(mode=0o700)
                runner.raw_directory = raw
                identity_file = raw / "service.identity"
                identity_file.write_text("123|100\n", encoding="ascii")
                identity_file.chmod(0o600)
                adapter = self._adapter(platform, runner, identity_file=identity_file)
                assert adapter.service is not None
                service = adapter.service
                observed_sidecars: list[str] = []
                verify = service._verify_candidate_pid

                def verify_after_sidecar_invalidation(timeout: float):
                    observed_sidecars.append(identity_file.read_text(encoding="utf-8"))
                    return verify(timeout)

                with mock.patch.object(
                    service, "_verify_candidate_pid", side_effect=verify_after_sidecar_invalidation
                ):
                    with mock.patch.object(
                        service, "_write_pid", side_effect=OSError("injected pid-file failure")
                    ):
                        with self.assertRaises(OSError):
                            service._start(5.0)
                self.assertEqual(observed_sidecars, ["pending\n"])
                self.assertIsNotNone(service._replacement_identity)
                self.assertEqual(identity_file.read_text(encoding="ascii").strip(), "456|100")
                # The catch/finalizer path must stop the proven replacement,
                # even though the authoritative PID file was not updated.
                adapter.finalize(5.0)

    def test_desktop_identity_file_is_explicit_not_ambient_environment(self) -> None:
        for platform in ("windows",):
            with self.subTest(platform=platform):
                runner = HostedDesktopProcessRunner(platform=platform, binary=self.cli)
                runner.raw_directory = Path(self.directory.name) / f"{platform}-explicit-raw"
                runner.raw_directory.mkdir(mode=0o700)
                identity_file = runner.raw_directory / "explicit.identity"
                ambient_file = runner.raw_directory / "ambient.identity"
                identity_file.write_text("123|100\n", encoding="ascii")
                identity_file.chmod(0o600)
                with mock.patch.dict(
                    os.environ, {"SERVICE_IDENTITY_FILE": str(ambient_file)}, clear=False
                ):
                    adapter = self._adapter(platform, runner, identity_file=identity_file)
                assert adapter.service is not None
                self.assertEqual(adapter.service.identity_file, identity_file)
                self.assertTrue(identity_file.exists())
                self.assertFalse(ambient_file.exists())

    def test_desktop_process_seam_rejects_incomplete_or_invalid_configuration(self) -> None:
        runner = HostedDesktopProcessRunner(platform="windows", binary=self.cli)
        runner.raw_directory = Path(self.directory.name) / "invalid-raw"
        with self.assertRaisesRegex(HostedAdapterError, "SERVICE_CONTROL_INCOMPLETE"):
            WindowsHostedAdapter(
                cli=self.cli, profile=self.profile, runner=runner, service_pid=123
            )
        for adapter_class in (WindowsHostedAdapter, MacOSHostedAdapter):
            with self.assertRaisesRegex(HostedAdapterError, "SERVICE_CONTROL_INCOMPLETE"):
                adapter_class(
                    cli=self.cli,
                    profile=self.profile,
                    runner=runner,
                    service_binary=self.cli,
                    service_pid_file=Path(self.directory.name) / "incomplete.pid",
                    service_socket=(
                        Path("127.0.0.1:50051")
                        if adapter_class is WindowsHostedAdapter
                        else Path(self.directory.name) / "control.sock"
                    ),
                )
        with self.assertRaisesRegex(HostedAdapterError, "SERVICE_CONTROL_ADDRESS_INVALID"):
            WindowsHostedAdapter(
                cli=self.cli,
                profile=self.profile,
                runner=runner,
                service_pid=123,
                service_binary=self.cli,
                service_pid_file=Path(self.directory.name) / "invalid-windows.pid",
                service_socket=Path("0.0.0.0:50051"),
            )


    def test_macos_default_control_socket_matches_product_runtime_layout(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertEqual(
                _default_control_socket(),
                Path("/var/run/dobbyvpn/control.sock"),
            )
        with mock.patch.dict(
            os.environ, {"DOBBYVPN_CONTROL_SOCKET": "/tmp/runtime/control.sock"}, clear=True
        ):
            self.assertEqual(_default_control_socket(), Path("/tmp/runtime/control.sock"))

    def test_macos_workflow_uses_disposable_daemon_scratch(self) -> None:
        workflow = (
            Path(__file__).resolve().parents[3]
            / ".github"
            / "workflows"
            / "qualification_platform.yml"
        ).read_text(encoding="utf-8")
        self.assertIn('"$RUN_DIR/service.log.stdout"', workflow)
        self.assertIn('"$RUN_DIR/service.log.stderr"', workflow)
        self.assertIn('rm -rf "$RUN_DIR"', workflow)
        self.assertNotIn("macos-launchd-service.stdout", workflow)
        self.assertNotIn("macos-launchd-service.stderr", workflow)
        self.assertNotIn("append_macos_daemon_stream", workflow)
if __name__ == "__main__":
    unittest.main()
