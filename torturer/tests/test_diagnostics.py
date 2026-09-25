from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from io import BytesIO, StringIO
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

from torturer_checks.diagnostics import add_stream_notes, emit_streams


PRODUCT_ROOT = Path(__file__).resolve().parents[2]


def _load_script(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise AssertionError(f"could not load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()

    def flush(self) -> None:
        pass


class DiagnosticPreservationTests(unittest.TestCase):
    def test_command_streams_are_forwarded_byte_for_byte(self) -> None:
        stdout = b'Password="profile-credential"\x00\xff\n'
        stderr = b"token=private-token\n"
        destination = BinaryStderr()

        emit_streams("test-command", stdout, stderr, stream=destination)

        rendered = destination.buffer.getvalue()
        self.assertIn(b"[test-command stdout]\n" + stdout, rendered)
        self.assertIn(b"[test-command stderr]\n" + stderr, rendered)

    def test_exception_notes_keep_profile_values_and_invalid_bytes_visible(self) -> None:
        error = RuntimeError("test failed")
        payload = b"credential=private-value\x00\xff"

        add_stream_notes(error, "command", payload, None)

        self.assertIn("credential=private-value", error.__notes__[0])
        self.assertIn("\x00", error.__notes__[0])
        self.assertIn(r"\xff", error.__notes__[0])

    def test_hosted_collection_copies_and_forwards_original_text_bytes(self) -> None:
        collector = _load_script(
            "dobbyvpn_collect_diagnostics",
            PRODUCT_ROOT / ".github/scripts/collect_diagnostics.py",
        )
        payload = b'Password="hosted-profile-value"\x00\xff\n'
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            source = root / "source"
            source.mkdir()
            (source / "test.log").write_bytes(payload)
            output = root / "diagnostics"
            stdout = StringIO()
            stderr = BinaryStderr()

            with redirect_stdout(stdout), redirect_stderr(stderr):
                records = collector.collect([source], output)

            self.assertEqual((output / "source/test.log").read_bytes(), payload)
            self.assertEqual(records[0]["bytes"], len(payload))
            self.assertEqual(records[0]["sha256"], collector._sha256(payload))
            self.assertEqual(
                set(records[0]),
                {"path", "kind", "mime_type", "mime", "bytes", "sha256"},
            )
            self.assertNotIn("source_bytes", records[0])
            self.assertIn(payload, stderr.buffer.getvalue())
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual(manifest["files"][0], records[0])

    def test_clipboard_payload_is_kept_out_of_diagnostics(self) -> None:
        smoke = _load_script(
            "dobbyvpn_native_ui_smoke",
            PRODUCT_ROOT / ".github/scripts/native_ui_smoke.py",
        )
        payload = b"clipboard-profile-value"
        destination = BinaryStderr()
        completed = __import__("subprocess").CompletedProcess(
            ["pbpaste"], 0, stdout=None, stderr=b"clipboard read warning\n",
        )

        def run(_command, **kwargs):
            kwargs["stdout"].write(payload)
            return completed

        with mock.patch.object(smoke.subprocess, "run", side_effect=run):
            with redirect_stderr(destination):
                result, captured = smoke._clipboard_payload(["pbpaste"], "clipboard", 5)

        self.assertIs(result, completed)
        self.assertEqual(captured, payload)
        self.assertIn(b"clipboard read warning", destination.buffer.getvalue())
        self.assertNotIn(payload, destination.buffer.getvalue())

    def test_native_ui_subprocess_streams_are_forwarded_byte_for_byte(self) -> None:
        smoke = _load_script(
            "dobbyvpn_native_ui_smoke_streams",
            PRODUCT_ROOT / ".github/scripts/native_ui_smoke.py",
        )
        stdout = b'Password="native-profile-value"\x00\xff\n'
        stderr = b"token=native-token\n"
        completed = __import__("subprocess").CompletedProcess(
            ["native-command"], 7, stdout=stdout, stderr=stderr,
        )
        destination = BinaryStderr()

        with mock.patch.object(smoke.subprocess, "run", return_value=completed):
            with redirect_stderr(destination):
                result = smoke._native_run(["native-command"], capture_output=True)

        self.assertIs(result, completed)
        forwarded = destination.buffer.getvalue()
        self.assertIn(stdout, forwarded)
        self.assertIn(stderr, forwarded)


if __name__ == "__main__":
    unittest.main()
