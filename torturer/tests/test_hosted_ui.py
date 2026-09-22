from __future__ import annotations

import hashlib
import json
import io
from pathlib import Path
import sys
import struct
import tempfile
import unittest
from unittest import mock
import zlib

from torturer_contract.functional.engine import ScenarioExecutionError
from torturer_checks.hosted.ui import HeadlessUIAdapter
from torturer_contract.functional.capabilities import Capability
from torturer_contract.functional.scenarios import ScenarioDefinition, ScenarioStep


def _png(width: int = 2, height: int = 1) -> bytes:
    signature = b"\x89PNG\r\n\x1a\n"

    def chunk(kind: bytes, payload: bytes) -> bytes:
        return (
            struct.pack(">I", len(payload))
            + kind
            + payload
            + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    row = b"\x00" + (b"\xff\x00\x00\xff" * width)
    return signature + chunk(b"IHDR", ihdr) + chunk(
        b"IDAT", zlib.compress(row * height)
    ) + chunk(b"IEND", b"")


class FakeBase:
    capabilities = frozenset(Capability)
    capability_unavailable_reasons: dict[Capability, str] = {}

    def __init__(self) -> None:
        self.selected = None
        self.baselines = 0
        self.native_preparations = 0
        self.reset_calls = 0
        self.finalize_calls = 0

    def set_progress_sink(self, _sink) -> None:
        pass

    def discover_connections(self, timeout_seconds: float = 30.0):
        from torturer_contract.functional.results import ConnectionIdentity

        return (
            ConnectionIdentity(index=0, protocol="OUTLINE"),
            ConnectionIdentity(index=1, protocol="XRAY"),
        )

    def select_connection(self, connection) -> None:
        self.selected = connection

    def _emit_progress(self, _event: str, **_fields: object) -> None:
        pass

    def _capture_baseline(self, _timeout: float) -> None:
        self.baselines += 1

    def prepare_native_connect(self, timeout: float) -> None:
        self.native_preparations += 1
        self._capture_baseline(timeout)

    def _connected(self, _timeout: float) -> bool:
        return True

    def _cleanup_verified(self, _timeout: float) -> bool:
        return True

    def execute(self, step: ScenarioStep) -> dict[str, object]:
        return {
            "tunnel_interface": True,
            "routing_verified": True,
            "cleanup_verified": True,
        } if step.operation in {"observe_tunnel", "observe_routing_identity", "inspect_cleanup"} else {}

    def reset(self, _timeout_seconds: float = 30.0) -> None:
        self.reset_calls += 1

    def finalize(self, _timeout_seconds: float = 30.0, *, deadline=None) -> None:
        self.finalize_calls += 1


class HeadlessUIAdapterTests(unittest.TestCase):
    def test_ui_actions_use_one_auto_selected_connection_and_delegate_observations(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile.toml"
            profile.write_text("[[Outline]]\nendpoint = 'example.invalid'\npassword = 'secret-value'\n", encoding="utf-8")
            companion = root / "companion.py"
            companion.write_text(
                "#!/usr/bin/env python3\n"
                "import json, sys\n"
                "for line in sys.stdin:\n"
                " request=json.loads(line)\n"
                " op=request.get('op')\n"
                " print('companion diagnostic secret-value', file=sys.stderr, flush=True)\n"
                " if op == 'configure': response={'ok': True, 'status': 'Ready', 'button': 'Connect'}\n"
                " elif op == 'connect': response={'ok': True, 'status': 'Connected', 'button': 'Disconnect'}\n"
                " elif op == 'disconnect': response={'ok': True, 'status': 'Disconnected', 'button': 'Connect'}\n"
                " elif op == 'close': response={'ok': True, 'status': 'Disconnected', 'button': 'Connect'}\n"
                " elif op == 'wait': response={'ok': True, 'status': request.get('state', 'Disconnected')}\n"
                " else: response={'ok': True, 'status': 'Disconnected'}\n"
                " print(json.dumps(response), flush=True)\n"
                " if op == 'close': break\n",
                encoding="utf-8",
            )
            companion.chmod(0o700)
            runner = mock.Mock(raw_directory=root / "raw", environment=dict())
            base = FakeBase()
            adapter = HeadlessUIAdapter(base=base, ui_test=companion, profile=profile, runner=runner)
            connection = adapter.discover_connections()[0]
            self.assertEqual(connection.protocol, "AUTO")
            self.assertEqual(connection.index, 0)
            adapter.select_connection(connection)
            scenario = ScenarioDefinition(
                id="functional.ui-smoke",
                steps=(
                    ScenarioStep(id="configure", operation="configure", timeout_seconds=8),
                    ScenarioStep(id="connect", operation="connect", timeout_seconds=8),
                    ScenarioStep(id="tunnel", operation="observe_tunnel", timeout_seconds=8),
                    ScenarioStep(id="disconnect", operation="disconnect", timeout_seconds=8),
                    ScenarioStep(id="reconnect", operation="reconnect", timeout_seconds=8),
                    ScenarioStep(id="final-disconnect", operation="disconnect", timeout_seconds=8),
                ),
                required_capabilities=frozenset(),
                assertion_ids=("configure.accepted",),
                max_duration_seconds=60,
            )
            with mock.patch("sys.stderr", new_callable=io.StringIO) as diagnostics:
                with mock.patch.object(adapter, "_capture", return_value=root / "capture.png") as capture:
                    observations = adapter.execute_scenario(scenario)
                    adapter.reset()
                    adapter.finalize()
            self.assertTrue(observations["configured"])
            self.assertTrue(observations["tunnel_interface"])
            self.assertEqual(base.native_preparations, 2)
            self.assertEqual(base.baselines, 2)
            labels = [call.args[0] for call in capture.call_args_list]
            self.assertIn("startup", labels)
            self.assertIn("settings", labels)
            self.assertIn("configured", labels)
            self.assertIn("connected", labels)
            self.assertIn("disconnected", labels)
            self.assertIn("final", labels)
            self.assertEqual(base.reset_calls, 1)
            self.assertEqual(base.finalize_calls, 1)
            self.assertIsNone(adapter._process)
            self.assertIn("companion diagnostic", diagnostics.getvalue())
            self.assertIn("[REDACTED]", diagnostics.getvalue())
            self.assertNotIn("secret-value", diagnostics.getvalue())

    def test_binary_streams_redact_split_secret_and_preserve_invalid_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile.toml"
            profile.write_text("password = 'secret-value'\n", encoding="utf-8")
            companion = root / "companion.py"
            companion.write_text(
                "#!/usr/bin/env python3\n"
                "import json, sys, time\n"
                "for raw in sys.stdin.buffer:\n"
                " request=json.loads(raw)\n"
                " sys.stderr.buffer.write(b'diagnostic secret-')\n"
                " sys.stderr.buffer.flush()\n"
                " time.sleep(0.05)\n"
                " sys.stderr.buffer.write(b'value invalid:\\xff\\n')\n"
                " sys.stderr.buffer.flush()\n"
                " response={'ok': True, 'status': 'Ready'}\n"
                " if request.get('op') == 'close': response['status']='Disconnected'\n"
                " sys.stdout.buffer.write((json.dumps(response)+'\\n').encode())\n"
                " sys.stdout.buffer.flush()\n"
                " if request.get('op') == 'close': break\n",
                encoding="utf-8",
            )
            companion.chmod(0o700)
            runner = mock.Mock(raw_directory=root / "raw", environment={})
            adapter = HeadlessUIAdapter(
                base=FakeBase(), ui_test=companion, profile=profile, runner=runner,
            )
            diagnostics = io.StringIO()
            with mock.patch("sys.stderr", diagnostics):
                self.assertTrue(adapter._request({"op": "configure"}, 2).ok)
                adapter._close_process(2, deadline=None)
            rendered = diagnostics.getvalue()
            self.assertIn("[headless-ui stdout begin]", rendered)
            self.assertIn("[headless-ui stderr end]", rendered)
            self.assertIn("[REDACTED]", rendered)
            self.assertNotIn("secret-value", rendered)
            self.assertIn(r"invalid:\xff", rendered)

    def test_malformed_stdout_fails_closed_and_is_rendered_reversibly(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile.toml"
            profile.write_text("password = 'secret-value'\n", encoding="utf-8")
            companion = root / "companion.py"
            companion.write_text(
                "#!/usr/bin/env python3\n"
                "import sys\n"
                "sys.stdout.buffer.write(b'\\xff\\n')\n"
                "sys.stdout.buffer.flush()\n",
                encoding="utf-8",
            )
            companion.chmod(0o700)
            runner = mock.Mock(raw_directory=root / "raw", environment={})
            adapter = HeadlessUIAdapter(
                base=FakeBase(), ui_test=companion, profile=profile, runner=runner,
            )
            diagnostics = io.StringIO()
            with mock.patch("sys.stderr", diagnostics):
                with self.assertRaisesRegex(ScenarioExecutionError, "UI_RESPONSE_INVALID"):
                    adapter._request({"op": "configure"}, 2)
                try:
                    adapter._close_process(2, deadline=None)
                except ScenarioExecutionError:
                    # The malformed-output fixture exits before it can
                    # acknowledge the cleanup request; the primary protocol
                    # failure above remains the assertion under test.
                    pass
            self.assertIn(r"\xff", diagnostics.getvalue())
            self.assertNotIn("�", diagnostics.getvalue())

    def test_capture_requires_complete_png_and_matching_producer_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile.toml"
            profile.write_text("password = 'secret-value'\n", encoding="utf-8")
            companion = root / "companion.py"
            companion.write_text("", encoding="utf-8")
            companion.chmod(0o700)
            runner = mock.Mock(raw_directory=root / "raw", environment={})
            adapter = HeadlessUIAdapter(
                base=FakeBase(), ui_test=companion, profile=profile, runner=runner,
            )
            directory_path = runner.raw_directory / "screenshots" / "desktop-mini"
            directory_path.mkdir(parents=True)
            path = directory_path / "001-startup.png"
            payload = _png()
            path.write_bytes(payload)
            valid = {
                "ok": True,
                "mime": "image/png",
                "path": str(path),
                "bytes": len(payload),
                "sha256": hashlib.sha256(payload).hexdigest(),
                "width": 2,
                "height": 1,
            }
            with mock.patch.object(adapter, "_request", return_value=mock.Mock(**valid)):
                self.assertEqual(adapter._capture("startup"), path)

            corrupt = bytearray(payload)
            corrupt[-1] ^= 1
            path.write_bytes(corrupt)
            corrupt_response = dict(valid)
            corrupt_response.update(
                bytes=len(corrupt), sha256=hashlib.sha256(corrupt).hexdigest()
            )
            adapter._capture_counter = 0
            with mock.patch.object(
                adapter, "_request", return_value=mock.Mock(**corrupt_response)
            ):
                with self.assertRaisesRegex(
                    ScenarioExecutionError, "UI_CAPTURE_INTEGRITY_FAILED"
                ):
                    adapter._capture("startup")

            path.write_bytes(payload)
            mismatch = dict(valid, sha256="0" * 64)
            adapter._capture_counter = 0
            with mock.patch.object(adapter, "_request", return_value=mock.Mock(**mismatch)):
                with self.assertRaisesRegex(
                    ScenarioExecutionError, "UI_CAPTURE_INTEGRITY_FAILED"
                ):
                    adapter._capture("startup")


if __name__ == "__main__":
    unittest.main()
