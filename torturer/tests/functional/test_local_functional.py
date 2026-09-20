from __future__ import annotations

import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest import mock

from torturer_checks.functional import (
    _local_exit_code,
    _create_log,
    build_parser,
    main,
    _supervised_request_root,
    _prepare_output_path,
)
from torturer_checks.hosted.cli import (
    HostedAdapterError,
    SubprocessRunner,
    _allocate_evidence_path,
)
from torturer_checks.hosted.run import _write_json
from torturer_contract.functional.results import ConnectionIdentity
from torturer_contract.functional.scenarios import (
    test_set as canonical_test_set,
)


class LocalRunTests(unittest.TestCase):
    CONNECTION = ConnectionIdentity(0, "OUTLINE")

    def test_parser_exposes_only_canonical_runner_options(self) -> None:
        options = {
            option
            for action in build_parser()._actions
            for option in action.option_strings
        }
        self.assertTrue({"--output", "--raw-log-dir", "--scenario"}.issubset(options))
        self.assertTrue(
            {"--result", "--logs", "--scenario-id", "--adapter", "--dobby-source"}.isdisjoint(options)
        )
        self.assertIn("--suite", options)
        self.assertEqual(build_parser().parse_args(
            ["--platform", "linux", "--profile", "profile", "--output", "result",
             "--raw-log-dir", "logs"]
        ).suite, "mini")

    def test_full_direct_functional_entrypoint_rejected_before_side_effects(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text("profile", encoding="utf-8")
            for platform in ("linux", "windows", "macos", "android"):
                with self.subTest(platform=platform), mock.patch(
                    "torturer_checks.functional.adapter_for_platform"
                ) as setup:
                    with self.assertRaisesRegex(
                        ValueError, "FULL_SUITE_REQUIRES_LOCAL_VM_ORCHESTRATOR"
                    ):
                        main([
                            "--platform", platform, "--suite", "full",
                            "--profile", str(profile), "--output", str(root / f"{platform}.json"),
                            "--raw-log-dir", str(root / f"{platform}-logs"),
                        ])
                    setup.assert_not_called()
                    self.assertFalse((root / f"{platform}-logs").exists())

    @staticmethod
    def _result(scenario):
        return {
            "environment": {"platform": "linux", "platform_version": "test", "architecture": "amd64"},
            "connection": LocalRunTests.CONNECTION.to_dict(),
            "scenario": {"id": scenario.id},
            "outcome": "passed",
            "assertions": [],
            "cleanup": {"required": False, "verified": True},
            "measurements": {},
            "timing": {"elapsed_ms": 0},
        }

    def test_local_lane_accepts_the_explicit_session_deadline(self) -> None:
        from torturer_checks.functional import _parse_timeout
        import argparse

        self.assertEqual(_parse_timeout("7200"), 7200.0)
        for value in ("0", "-1", "nan", "inf"):
            with self.subTest(value=value), self.assertRaises(argparse.ArgumentTypeError):
                _parse_timeout(value)

    def test_local_exit_code_only_needs_passing_results(self) -> None:
        self.assertEqual(_local_exit_code([{"outcome": "passed"}]), 0)
        self.assertEqual(_local_exit_code([]), 2)
        self.assertEqual(_local_exit_code([{"outcome": "failed"}]), 2)
        self.assertEqual(_local_exit_code([{"outcome": "unavailable"}]), 2)

    def test_setup_error_without_connection_inventory_propagates(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text("profile", encoding="utf-8")
            profile.chmod(0o600)
            output = root / "result.json"
            error = HostedAdapterError("CLI_UNAVAILABLE exact setup failure")
            with (
                mock.patch(
                    "torturer_checks.functional.adapter_for_platform",
                    side_effect=error,
                ),
                self.assertRaises(HostedAdapterError) as raised,
            ):
                main(
                    [
                        "--platform", "macos",
                        "--profile", str(profile),
                        "--output", str(output),
                        "--raw-log-dir", str(root / "logs"),
                    ]
                )
            self.assertIs(raised.exception, error)
            self.assertFalse(output.exists())

    def test_other_setup_error_propagates_without_a_substitute_result(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text("profile", encoding="utf-8")
            profile.chmod(0o600)
            output = root / "result.json"
            error = HostedAdapterError("CLI_UNAVAILABLE exact setup failure")
            with (
                mock.patch(
                    "torturer_checks.functional.adapter_for_platform",
                    side_effect=error,
                ),
                self.assertRaises(HostedAdapterError) as raised,
            ):
                main(
                    [
                        "--platform", "macos",
                        "--profile", str(profile),
                        "--output", str(output),
                        "--raw-log-dir", str(root / "logs"),
                    ]
                )
            self.assertIs(raised.exception, error)
            self.assertFalse(output.exists())

    def test_main_creates_required_logs_before_candidate_setup_and_retains_observations(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text("profile", encoding="utf-8")
            profile.chmod(0o600)
            output = root / "result.json"
            firewall_helper = root / "routing-probe-firewall"
            firewall_helper.write_text("fixed", encoding="ascii")
            firewall_helper.chmod(0o700)
            selected = canonical_test_set()
            reset = mock.Mock()
            results = [self._result(scenario) for scenario in selected]
            adapter = SimpleNamespace(
                adapter_id="test-adapter",
                capabilities=frozenset(),
                select_connection=lambda _connection: None,
                reset=reset,
            )

            def setup(*_args, **_kwargs):
                logs = root / "logs"
                self.assertTrue((logs / "app.log").is_file())
                self.assertTrue((logs / "service.log").is_file())
                (logs / "app.log").write_bytes(b"VPN application started\n")
                return adapter

            with (
                mock.patch("torturer_checks.functional.adapter_for_platform", side_effect=setup),
                mock.patch(
                    "torturer_checks.hosted.run._discover_connections",
                    return_value=(self.CONNECTION,),
                ),
                mock.patch(
                    "torturer_checks.hosted.run._run_connection_matrix",
                    return_value=results,
                ),
                mock.patch("torturer_checks.hosted.run._finalize_adapter"),
            ):
                code = main(
                    [
                        "--platform", "linux",
                        "--profile", str(profile),
                        "--output", str(output),
                        "--raw-log-dir", str(root / "logs"),
                        "--routing-firewall-helper", str(firewall_helper),
                    ]
                )
            self.assertEqual(code, 0)
            reset.assert_not_called()

    def test_selected_diagnostic_resets_adapter_before_connection_discovery(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text("profile", encoding="utf-8")
            profile.chmod(0o600)
            output = root / "result.json"
            logs = root / "logs"
            firewall_helper = root / "routing-probe-firewall"
            firewall_helper.write_text("fixed", encoding="ascii")
            firewall_helper.chmod(0o700)
            scenario = canonical_test_set()[0]
            results = [self._result(scenario)]
            events: list[str] = []

            def reset() -> None:
                events.append("reset")

            adapter = SimpleNamespace(
                adapter_id="test-adapter",
                reset=reset,
            )

            def setup(*_args, **_kwargs):
                events.append("setup")
                (logs / "app.log").write_bytes(b"VPN application started\n")
                return adapter

            def discover(*_args, **_kwargs):
                events.append("discover")
                return (self.CONNECTION,)

            with (
                mock.patch(
                    "torturer_checks.functional.adapter_for_platform",
                    side_effect=setup,
                ),
                mock.patch(
                    "torturer_checks.hosted.run._discover_connections",
                    side_effect=discover,
                ),
                mock.patch(
                    "torturer_checks.hosted.run._run_connection_matrix",
                    return_value=results,
                ),
                mock.patch("torturer_checks.hosted.run._finalize_adapter"),
            ):
                code = main(
                    [
                        "--platform", "linux",
                        "--profile", str(profile),
                        "--output", str(output),
                        "--raw-log-dir", str(logs),
                        "--routing-firewall-helper", str(firewall_helper),
                        "--scenario", scenario.id,
                    ]
                )

            self.assertEqual(code, 0)
            self.assertLess(events.index("reset"), events.index("discover"))

    def test_failed_selected_reset_preserves_error_and_application_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            request = root / "request"
            (request / "input").mkdir(parents=True)
            (request / "output").mkdir()
            profile = request / "input" / "profile.toml"
            profile.write_text("profile", encoding="utf-8")
            profile.chmod(0o600)
            output = request / "output" / "result.json"
            logs = request / "logs"
            firewall_helper = root / "routing-probe-firewall"
            firewall_helper.write_text("fixed", encoding="ascii")
            firewall_helper.chmod(0o700)
            scenario = canonical_test_set()[0]
            error = HostedAdapterError("HELD_SESSION_RESET_FAILED")

            def reset() -> None:
                self.assertTrue((logs / "app.log").is_file())
                self.assertTrue((logs / "service.log").is_file())
                raise error

            adapter = SimpleNamespace(
                adapter_id="test-adapter",
                reset=reset,
            )

            with (
                mock.patch.dict(
                    os.environ,
                    {
                        "DOBBYVPN_SUPERVISED_REQUEST": "1",
                        "DOBBYVPN_REQUEST_ROOT": str(request),
                    },
                    clear=False,
                ),
                mock.patch(
                    "torturer_checks.functional.adapter_for_platform",
                    return_value=adapter,
                ),
                mock.patch(
                    "torturer_checks.hosted.run._finalize_adapter"
                ) as finalize,
                self.assertRaises(HostedAdapterError) as raised,
            ):
                main(
                    [
                        "--platform", "linux",
                        "--profile", str(profile),
                        "--output", str(output),
                        "--raw-log-dir", str(logs),
                        "--routing-firewall-helper", str(firewall_helper),
                        "--scenario", scenario.id,
                    ]
                )

            self.assertIs(raised.exception, error)
            self.assertFalse(output.exists())
            finalize.assert_called_once()

class LocalLogTests(unittest.TestCase):
    def test_required_logs_are_created_before_setup(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            app = root / "app.log"
            service = root / "service.log"
            _create_log(app)
            _create_log(service)
            self.assertTrue(app.is_file())
            self.assertTrue(service.is_file())

    def test_result_path_can_replace_previous_attempt(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _prepare_output_path(root / "result.json")
            result = root / "result.json"
            result.write_text("old", encoding="utf-8")
            _prepare_output_path(result)

    def test_supervised_request_uses_runner_configured_paths(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            request = Path(temporary) / "request"
            input_directory = request / "input"
            logs = request / "logs"
            output = request / "output"
            logs.mkdir(parents=True)
            input_directory.mkdir()
            output.mkdir()
            # Named-user ACL masks appear in POSIX group mode bits even when
            # the owning runner keeps the ordinary group entry empty.
            request.chmod(0o710)
            input_directory.chmod(0o750)
            logs.chmod(0o700)
            output.chmod(0o770)
            profile = input_directory / "profile"
            app = logs / "app.log"
            service = logs / "service.log"
            profile.write_bytes(b"profile")
            app.write_bytes(b"app")
            service.write_bytes(b"service")
            profile.chmod(0o640)
            app.chmod(0o660)
            service.chmod(0o660)
            with mock.patch.dict(
                os.environ,
                {
                    "DOBBYVPN_SUPERVISED_REQUEST": "1",
                    "DOBBYVPN_REQUEST_ROOT": str(request),
                },
                clear=False,
            ):
                supervised = _supervised_request_root()
                self.assertEqual(supervised, request.resolve())
                _create_log(app)
                _create_log(service)
                _prepare_output_path(output / "functional.json")
                executable = str(Path(sys.executable).resolve())
                runner = SubprocessRunner(output)
                self.assertEqual(app.read_bytes(), b"app")
                existing = output / "supervised.raw.log"
                existing.write_bytes(b"earlier evidence")
                allocated = _allocate_evidence_path(
                    output, "supervised", ".raw.log"
                )
                self.assertEqual(allocated, output / "supervised-2.raw.log")
                self.assertTrue(allocated.is_file())
                self.assertEqual(existing.read_bytes(), b"earlier evidence")
                _write_json(output / "functional.json", {"kind": "test"})
                self.assertEqual(
                    json.loads((output / "functional.json").read_text()),
                    {"kind": "test"},
                )



if __name__ == "__main__":
    unittest.main()
