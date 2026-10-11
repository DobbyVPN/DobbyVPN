from __future__ import annotations

import json
import os
from io import BytesIO
from pathlib import Path
import tempfile
import textwrap
import threading
import time
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

from torturer_runner.ui import native_case
from torturer_runner.ui.native_case import _run_observed_case, run_desktop_case
from torturer_runner.ui.observations import for_base_adapter


class NativeCaseObservationProcessTests(unittest.TestCase):
    @unittest.skipIf(os.name == "nt", "real helper process fixture uses a POSIX executable script")
    def test_helper_request_gets_same_adapter_reply_and_exact_streams(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = self._helper(root, r'''
                import json, pathlib, sys
                request = json.loads(sys.stdin.buffer.readline())
                assert request["observer_enabled"] is True
                sys.stdout.buffer.write(b"helper-stdout\x00\xff\n")
                sys.stdout.buffer.flush()
                sys.stderr.buffer.write(b"helper-stderr\x00\xfe\n")
                sys.stderr.buffer.flush()
                asked = {"operation": "snapshot", "timeout_seconds": 30}
                sys.stdout.buffer.write(b"DOBBY_NATIVE_OBSERVE " + json.dumps(asked).encode() + b"\n")
                sys.stdout.buffer.flush()
                reply = json.loads(sys.stdin.buffer.readline())
                assert reply == {"ok": True, "value": {"state": "CONNECTED"}}, reply
                result = {
                    "case_id": request["case"], "outcome": "passed",
                    "assertions": [{"id": "connected.snapshot", "outcome": "passed"}],
                    "cleanup_errors": [],
                }
                pathlib.Path(request["result_path"]).write_text(json.dumps(result), encoding="utf-8")
            ''')
            app = root / "app"
            app.mkdir()
            requests = []
            forwarded_stdout = BytesIO()
            forwarded_stderr = BytesIO()

            def observer(request):
                requests.append(request)
                self.assertEqual(request["operation"], "snapshot")
                self.assertLessEqual(request["timeout_seconds"], 4)
                self.assertGreater(request["timeout_seconds"], 0)
                return {"state": "CONNECTED"}

            with (
                patch.object(
                    native_case.sys, "stdout", SimpleNamespace(buffer=forwarded_stdout),
                ),
                patch.object(
                    native_case.sys, "stderr", SimpleNamespace(buffer=forwarded_stderr),
                ),
            ):
                result = run_desktop_case(
                    helper, app / "DobbyVPN.exe", "connected-session", root / "case",
                    4, observer=observer,
                )

            self.assertEqual(result["outcome"], "passed")
            self.assertEqual(len(requests), 1)
            self.assertEqual(Path(result["stdout_path"]).read_bytes().splitlines()[0],
                             b"helper-stdout\x00\xff")
            self.assertIn(b"DOBBY_NATIVE_OBSERVE ", Path(result["stdout_path"]).read_bytes())
            self.assertEqual(Path(result["stderr_path"]).read_bytes(), b"helper-stderr\x00\xfe\n")
            self.assertIn(b"helper-stdout\x00\xff\n", forwarded_stdout.getvalue())
            self.assertIn(b"helper-stderr\x00\xfe\n", forwarded_stderr.getvalue())

    @unittest.skipIf(os.name == "nt", "real helper process fixture uses a POSIX executable script")
    def test_observer_exception_is_returned_with_traceback_and_cannot_pass(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = self._helper(root, r'''
                import json, pathlib, sys
                request = json.loads(sys.stdin.buffer.readline())
                asked = {"operation": "unknown-operation", "timeout_seconds": 20}
                sys.stdout.buffer.write(b"DOBBY_NATIVE_OBSERVE " + json.dumps(asked).encode() + b"\n")
                sys.stdout.buffer.flush()
                reply = json.loads(sys.stdin.buffer.readline())
                assert reply["ok"] is False
                assert "ValueError: unsupported native observation operation" in reply["error"]
                result = {
                    "case_id": request["case"], "outcome": "passed",
                    "assertions": [{"id": "child.must-not-pass", "outcome": "passed"}],
                    "cleanup_errors": [],
                }
                pathlib.Path(request["result_path"]).write_text(json.dumps(result), encoding="utf-8")
            ''')
            app = root / "app"
            app.mkdir()
            result = run_desktop_case(
                helper, app / "DobbyVPN.exe", "connected-session", root / "case", 4,
                observer=lambda request: (_ for _ in ()).throw(
                    ValueError(f"unsupported native observation operation: {request['operation']}")
                ),
            )

            self.assertEqual(result["outcome"], "failed")
            self.assertIn("ValueError: unsupported native observation operation", result["error"])
            stderr = Path(result["stderr_path"]).read_bytes()
            self.assertIn(b"Traceback (most recent call last)", stderr)
            self.assertIn(b"ValueError: unsupported native observation operation", stderr)

    @unittest.skipIf(os.name == "nt", "real helper process fixture uses a POSIX executable script")
    def test_real_nonzero_helper_cannot_pass_and_cleanup_errors_are_retained(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = self._helper(root, r'''
                import json, pathlib, sys
                request = json.loads(sys.stdin.buffer.readline())
                sys.stdout.buffer.write(b"final helper output\x00\xff\n")
                sys.stdout.buffer.flush()
                sys.stderr.buffer.write(b"final helper stderr\x00\xfe\n")
                sys.stderr.buffer.flush()
                result = {
                    "case_id": request["case"], "outcome": "passed",
                    "assertions": [{"id": "child.pass", "outcome": "passed"}],
                    "cleanup_errors": ["native app did not exit"],
                }
                pathlib.Path(request["result_path"]).write_text(json.dumps(result), encoding="utf-8")
                sys.exit(9)
            ''')
            app = root / "app"
            app.mkdir()
            result = run_desktop_case(helper, app / "DobbyVPN.exe", "connected-session", root / "case", 4)

            self.assertEqual(result["outcome"], "failed")
            self.assertEqual(result["process_exit_code"], 9)
            self.assertEqual(result["cleanup_errors"], ["native app did not exit"])
            self.assertIn(b"final helper output\x00\xff\n", Path(result["stdout_path"]).read_bytes())
            self.assertEqual(Path(result["stderr_path"]).read_bytes(), b"final helper stderr\x00\xfe\n")

    @unittest.skipIf(os.name == "nt", "real helper process fixture uses a POSIX executable script")
    def test_deadline_terminates_child_and_keeps_both_original_streams(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = self._helper(root, r'''
                import sys, time
                sys.stdout.buffer.write(b"stdout-before-timeout\x00\xff\n")
                sys.stdout.buffer.flush()
                sys.stderr.buffer.write(b"stderr-before-timeout\x00\xfe\n")
                sys.stderr.buffer.flush()
                time.sleep(30)
            ''')
            app = root / "app"
            app.mkdir()
            directory = root / "case"
            request = {"operation": "native-case", "case": "connected-session"}

            with self.assertRaisesRegex(RuntimeError, "native helper timed out") as caught:
                _run_observed_case(helper, app / "DobbyVPN.exe", directory, request, 0.5, 0,
                                   lambda _request: {})

            self.assertTrue(any(b"stdout-before-timeout\x00\xff" in Path(path).read_bytes()
                                for path in directory.glob("native-case*.stdout.log")))
            self.assertTrue(any(b"stderr-before-timeout\x00\xfe" in Path(path).read_bytes()
                                for path in directory.glob("native-case*.stderr.log")))
            notes = "\n".join(caught.exception.__notes__)
            self.assertIn("stdout-before-timeout", notes)
            self.assertIn("stderr-before-timeout", notes)

    @unittest.skipIf(os.name == "nt", "real helper process fixture uses a POSIX executable script")
    def test_deadline_waits_for_late_observer_before_closing_diagnostic_logs(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = self._helper(root, r'''
                import json, sys, time
                sys.stdin.buffer.readline()
                request = {"operation": "snapshot", "timeout_seconds": 30}
                sys.stdout.buffer.write(b"DOBBY_NATIVE_OBSERVE " + json.dumps(request).encode() + b"\n")
                sys.stdout.buffer.flush()
                sys.stdin.buffer.readline()
                time.sleep(30)
            ''')
            app = root / "app"
            app.mkdir()
            directory = root / "case"
            callback_started = threading.Event()

            def slow_observer(_request):
                callback_started.set()
                time.sleep(0.3)  # Deliberately outlives this short case deadline.
                raise ValueError("late observer diagnostic")

            with self.assertRaisesRegex(RuntimeError, "native helper timed out") as caught:
                _run_observed_case(
                    helper, app / "DobbyVPN.exe", directory,
                    {"operation": "native-case", "case": "connected-session"},
                    0.15, 0, slow_observer,
                )

            self.assertTrue(callback_started.is_set())
            self.assertIs(caught.exception.observer_quiescent, True)
            stderr = b"".join(path.read_bytes() for path in directory.glob("native-case*.stderr.log"))
            self.assertIn(b"ValueError: late observer diagnostic", stderr)
            self.assertIn(b"Traceback (most recent call last)", stderr)
            self.assertIn("late observer diagnostic", "\n".join(caught.exception.__notes__))

    @unittest.skipIf(os.name == "nt", "real helper process fixture uses a POSIX executable script")
    def test_timed_out_active_observer_marks_backend_cleanup_unconfirmed(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = self._helper(root, r'''
                import json, sys, time
                sys.stdin.buffer.readline()
                request = {"operation": "snapshot", "timeout_seconds": 30}
                sys.stdout.buffer.write(b"DOBBY_NATIVE_OBSERVE " + json.dumps(request).encode() + b"\n")
                sys.stdout.buffer.flush()
                sys.stdin.buffer.readline()
                time.sleep(30)
            ''')
            app = root / "app"
            app.mkdir()
            directory = root / "case"
            callback_started = threading.Event()
            release_callback = threading.Event()
            callback_finished = threading.Event()

            def blocked_observer(_request):
                callback_started.set()
                try:
                    release_callback.wait()
                    return {"state": "CONNECTED"}
                finally:
                    callback_finished.set()

            with self.assertRaisesRegex(RuntimeError, "native helper timed out") as caught:
                _run_observed_case(
                    helper, app / "DobbyVPN.exe", directory,
                    {"operation": "native-case", "case": "connected-session"},
                    0.1, 0, blocked_observer,
                )

            self.assertTrue(callback_started.is_set())
            self.assertIs(caught.exception.observer_quiescent, False)
            self.assertIn("backend cleanup is unconfirmed", "\n".join(caught.exception.__notes__))
            release_callback.set()
            self.assertTrue(callback_finished.wait(2))

    @staticmethod
    def _helper(root: Path, body: str) -> Path:
        helper = root / "native-helper"
        helper.write_text("#!/usr/bin/env python3\n" + textwrap.dedent(body), encoding="utf-8")
        helper.chmod(0o755)
        return helper


class BaseAdapterObservationTests(unittest.TestCase):
    def test_observe_runs_the_existing_four_semantic_adapter_operations(self) -> None:
        base = Mock()
        base.execute.side_effect = [
            {"tunnel_interface": True},
            {"routing_verified": True},
            {"stability_verified": True, "stability_sample_count": 2},
            {"latency_ms": 2, "download_mbps": 3, "upload_mbps": 4},
        ]

        result = for_base_adapter(base)({"operation": "observe", "timeout_seconds": 8})

        self.assertEqual(result, {
            "tunnel_interface": True,
            "routing_verified": True,
            "stability_verified": True,
            "stability_sample_count": 2,
            "latency_ms": 2,
            "download_mbps": 3,
            "upload_mbps": 4,
            "throughput_positive": True,
        })
        self.assertEqual(
            [call.args[0].operation for call in base.execute.call_args_list],
            ["observe_tunnel", "observe_routing_identity", "measure_stability", "measure_throughput"],
        )
        self.assertTrue(all(1 <= call.args[0].timeout_seconds <= 8
                            for call in base.execute.call_args_list))
        base.configure.assert_not_called()
        base.connect.assert_not_called()

    def test_throughput_positive_rejects_zero_nan_and_non_numeric_metrics(self) -> None:
        for field in ("latency_ms", "download_mbps", "upload_mbps"):
            for invalid in (0, float("nan"), "4"):
                with self.subTest(field=field, invalid=invalid):
                    base = Mock()
                    throughput = {"latency_ms": 2, "download_mbps": 3, "upload_mbps": 4}
                    throughput[field] = invalid
                    base.execute.side_effect = [
                        {"tunnel_interface": True},
                        {"routing_verified": True},
                        {"stability_verified": True},
                        throughput,
                    ]

                    result = for_base_adapter(base)({"operation": "observe", "timeout_seconds": 8})

                    self.assertFalse(result["throughput_positive"])
                    # The observer response stays valid JSON even for invalid
                    # non-finite measurements, and retains their spelling.
                    encoded = json.dumps(result, allow_nan=False)
                    self.assertIn('"throughput_positive": false', encoded)
                    if isinstance(invalid, float):
                        self.assertEqual(result[field], "NaN")

    def test_known_lifecycle_requests_use_the_same_adapter_and_unknown_is_rejected(self) -> None:
        base = Mock()
        base._snapshot.return_value = {"state": "CONNECTED"}
        base._connected.return_value = False
        base._cleanup_verified.return_value = True
        base.restart_service_for_native_ui.return_value = {"process_loss_verified": True}
        observe = for_base_adapter(base)

        self.assertEqual(observe({"operation": "snapshot", "timeout_seconds": 3}),
                         {"state": "CONNECTED"})
        self.assertEqual(observe({"operation": "prepare-connect", "timeout_seconds": 3}),
                         {"prepared": True})
        self.assertEqual(observe({"operation": "cleanup", "timeout_seconds": 3}),
                         {"connected": False, "cleanup_verified": True})
        self.assertEqual(observe({"operation": "restart-service", "timeout_seconds": 3}),
                         {"process_loss_verified": True})
        self.assertEqual(observe({"operation": "reset", "timeout_seconds": 3}), {"reset": True})
        with self.assertRaisesRegex(ValueError, "unsupported native observation operation"):
            observe({"operation": "cli-start", "timeout_seconds": 3})
        base.execute.assert_not_called()
        base.configure.assert_not_called()
        base.connect.assert_not_called()
        base.start.assert_not_called()
        base.prepare_native_connect.assert_called_once_with(3.0)
        base.restart_service_for_native_ui.assert_called_once_with(3.0)
        base.reset.assert_called_once_with(timeout_seconds=3.0)


if __name__ == "__main__":
    unittest.main()
