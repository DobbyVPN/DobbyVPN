from __future__ import annotations

from contextlib import redirect_stderr
from io import BytesIO
from pathlib import Path
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_contract.functional.engine import ScenarioExecutionError
from torturer_checks.hosted.android import AndroidHostedAdapter
from torturer_checks.hosted.cli import CommandResult


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


class HostedAndroidFailureDiagnosticsTests(unittest.TestCase):
    @staticmethod
    def adapter_for(profile: Path, instrument: CommandResult) -> AndroidHostedAdapter:
        adapter = AndroidHostedAdapter.__new__(AndroidHostedAdapter)
        adapter.profile = profile
        adapter.ui_mode = "protocol-matrix"
        adapter._active_controls = ()
        adapter._run_instrumentation = mock.Mock(return_value=instrument)
        return adapter

    def run_failing_phase(
        self,
        adapter: AndroidHostedAdapter,
        profile: Path,
        adb_result: CommandResult | BaseException,
        forwarded: BinaryStderr,
    ) -> BaseException:
        command_file = profile.parent / "phase.command.json"
        output_name = "phase.observation.json"
        scenario = SimpleNamespace(id="android-diagnostic-phase")
        device_files: list[str] = []

        def adb(_arguments, _timeout, _failure_code, **_kwargs):
            if isinstance(adb_result, BaseException):
                raise adb_result
            return adb_result

        with (
            mock.patch.object(
                adapter,
                "_write_command",
                return_value=(command_file, "phase.profile", output_name),
            ),
            mock.patch.object(adapter, "_stage_private_file"),
            mock.patch.object(adapter, "_adb", side_effect=adb),
            redirect_stderr(forwarded),
        ):
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter._execute_phase(
                    scenario,
                    (),
                    time.monotonic() + 30,
                    device_files,
                )
        self.assertIn("phase.observation.json", device_files)
        return caught.exception

    def test_failed_instrumentation_forwards_exact_observation_bytes(self) -> None:
        instrument_stdout = b"INSTRUMENTATION_FAILED: test assertion\n"
        observation = b'{"command_output":"base64-diagnostics"}\x00\xff\n'
        instrument = CommandResult(
            command=("adb", "instrument"),
            returncode=1,
            stdout=instrument_stdout,
            stderr=b"instrumentation stderr\n",
        )
        output = CommandResult(
            command=("adb", "cat"),
            returncode=0,
            stdout=observation,
            stderr=b"observation cat stderr\n",
        )
        forwarded = BinaryStderr()

        with tempfile.TemporaryDirectory() as name:
            profile = Path(name) / "profile.toml"
            profile.write_bytes(b"synthetic profile\n")
            adapter = self.adapter_for(profile, instrument)
            failure = self.run_failing_phase(adapter, profile, output, forwarded)

        self.assertIn("Android instrumentation failed", str(failure))
        self.assertIn(observation, forwarded.buffer.getvalue())
        self.assertIn(b"[android-observation stdout]\n" + observation, forwarded.buffer.getvalue())
        self.assertIn(b"observation cat stderr\n", forwarded.buffer.getvalue())
        self.assertIn(b"instrumentation stderr\n", forwarded.buffer.getvalue())

    def test_observation_collection_error_is_attached_to_instrumentation_failure(self) -> None:
        instrument = CommandResult(
            command=("adb", "instrument"),
            returncode=1,
            stdout=b"INSTRUMENTATION_FAILED: original failure\n",
            stderr=b"original instrumentation stderr\n",
        )
        collection_error = ScenarioExecutionError(
            "ANDROID_OBSERVATION_UNAVAILABLE"
        )
        collection_error.stdout = b"cat stdout\x00\xff"
        collection_error.stderr = b"cat stderr diagnostic\n"
        collection_error.add_note("original collection detail")
        forwarded = BinaryStderr()

        with tempfile.TemporaryDirectory() as name:
            profile = Path(name) / "profile.toml"
            profile.write_bytes(b"synthetic profile\n")
            adapter = self.adapter_for(profile, instrument)
            failure = self.run_failing_phase(
                adapter, profile, collection_error, forwarded
            )

        self.assertIn("Android instrumentation failed", str(failure))
        notes = "\n".join(failure.__notes__)
        self.assertIn("ANDROID_OBSERVATION_UNAVAILABLE", notes)
        self.assertIn("original collection detail", notes)
        self.assertIn("cat stdout", notes)
        self.assertIn(r"\xff", notes)
        self.assertIn("cat stderr diagnostic", notes)

    def test_primary_and_both_cleanup_failures_keep_original_details(self) -> None:
        adapter = AndroidHostedAdapter.__new__(AndroidHostedAdapter)
        primary = ScenarioExecutionError("ANDROID_PRIMARY_SENTINEL")
        device_cleanup = ScenarioExecutionError("ANDROID_DEVICE_CLEANUP_SENTINEL")
        scratch_cleanup = ScenarioExecutionError("ANDROID_SCRATCH_CLEANUP_SENTINEL")
        device_cleanup.stderr = b"device cleanup stderr\x00\xff"
        scratch_cleanup.stdout = b"scratch cleanup stdout\n"
        scenario = SimpleNamespace(id="diagnostic", max_duration_seconds=30, steps=())

        with (
            mock.patch.object(adapter, "_execute_phase", side_effect=primary),
            mock.patch.object(adapter, "_cleanup_device", return_value=device_cleanup),
            mock.patch.object(adapter, "_cleanup_local_scratch", return_value=scratch_cleanup),
        ):
            with self.assertRaises(ScenarioExecutionError) as caught:
                adapter.execute_scenario(scenario)

        self.assertIs(caught.exception, primary)
        notes = "\n".join(primary.__notes__)
        self.assertIn("ANDROID_DEVICE_CLEANUP_SENTINEL", notes)
        self.assertIn("device cleanup stderr", notes)
        self.assertIn(r"\xff", notes)
        self.assertIn("ANDROID_SCRATCH_CLEANUP_SENTINEL", notes)
        self.assertIn("scratch cleanup stdout", notes)


if __name__ == "__main__":
    unittest.main()
