from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from io import BytesIO, StringIO
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import traceback
import unittest
from unittest import mock

from torturer_runner.diagnostics import add_stream_notes, emit_streams


PRODUCT_ROOT = Path(__file__).resolve().parents[3]


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

    def test_hosted_collection_copies_png_bytes_without_decoding_them(self) -> None:
        collector = _load_script(
            "dobbyvpn_collect_diagnostics_png",
            PRODUCT_ROOT / ".github/scripts/collect_diagnostics.py",
        )
        payload = b"\x89PNG\r\n\x1a\noriginal screenshot bytes"
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            source = root / "source"
            source.mkdir()
            (source / "screen.png").write_bytes(payload)
            output = root / "diagnostics"
            stdout = StringIO()
            stderr = BinaryStderr()

            with redirect_stdout(stdout), redirect_stderr(stderr):
                records = collector.collect([source], output)

            self.assertEqual((output / "source/screen.png").read_bytes(), payload)
            self.assertEqual(records[0]["kind"], "binary")
            self.assertEqual(records[0]["mime_type"], "image/png")
            self.assertEqual(records[0]["bytes"], len(payload))
            self.assertEqual(records[0]["sha256"], collector._sha256(payload))
            self.assertNotIn("width", records[0])
            self.assertNotIn("height", records[0])

    def test_native_ui_subprocess_streams_are_forwarded_byte_for_byte(self) -> None:
        smoke = _load_script(
            "dobbyvpn_native_ui_smoke_streams",
            PRODUCT_ROOT / "torturer/torturer_runner/ui/smoke.py",
        )
        stdout = b'Password="native-profile-value"\x00\xff\n'
        stderr = b"token=native-token\n"
        completed = __import__("subprocess").CompletedProcess(
            ["native-command"], 7, stdout=stdout, stderr=stderr,
        )
        destination = BinaryStderr()
        label = "native-helper operation=capture path=C:/screenshots/001-startup.png"

        with mock.patch.object(smoke, "run_finite_capture", return_value=completed):
            with redirect_stderr(destination):
                result = smoke._native_run(["native-command"], label=label, timeout_seconds=5)

        self.assertIs(result, completed)
        forwarded = destination.buffer.getvalue()
        self.assertIn(f"[{label} stdout]\n".encode() + stdout, forwarded)
        self.assertIn(f"[{label} stderr]\n".encode() + stderr, forwarded)

    def test_native_ui_timeout_forwards_tagged_partial_streams(self) -> None:
        smoke = _load_script(
            "dobbyvpn_native_ui_smoke_timeout_streams",
            PRODUCT_ROOT / "torturer/torturer_runner/ui/smoke.py",
        )
        subprocess = __import__("subprocess")
        stdout = b'{"ready":true}\x00\xff\n'
        stderr = b"native-ui-phase=capture-physical-start\n"
        failure = subprocess.TimeoutExpired(("native-command",), 10, output=stdout, stderr=stderr)
        destination = BinaryStderr()
        label = "native-helper operation=capture path=C:/screenshots/001-startup.png"

        with mock.patch.object(smoke, "run_finite_capture", side_effect=failure):
            with redirect_stderr(destination):
                with self.assertRaises(subprocess.TimeoutExpired) as caught:
                    smoke._native_run(["native-command"], label=label, timeout_seconds=10)

        self.assertIs(caught.exception, failure)
        forwarded = destination.buffer.getvalue()
        self.assertIn(f"[{label} stdout]\n".encode() + stdout, forwarded)
        self.assertIn(f"[{label} stderr]\n".encode() + stderr, forwarded)

    def test_hosted_read_failure_keeps_other_streams_and_original_error(self) -> None:
        collector = _load_script(
            "dobbyvpn_collect_read_failure", PRODUCT_ROOT / ".github/scripts/collect_diagnostics.py",
        )
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            source = root / "source"
            source.mkdir()
            denied = source / "service.log.lock"
            denied.touch()
            payload = b'original stderr credential="fixture"\x00\xff\n'
            (source / "service.stderr.log").write_bytes(payload)
            output = root / "diagnostics"
            original_read = Path.read_bytes
            error = PermissionError("original root-owned lock read error")

            def read(path):
                if path == denied:
                    raise error
                return original_read(path)

            stderr = BinaryStderr()
            with mock.patch.object(Path, "read_bytes", read), redirect_stdout(StringIO()), redirect_stderr(stderr):
                with self.assertRaises(collector.CollectionError) as caught:
                    collector.collect([root / "missing-source", source], output)

            self.assertEqual((output / "source/service.stderr.log").read_bytes(), payload)
            self.assertIn(payload, stderr.buffer.getvalue())
            manifest = json.loads((output / "manifest.json").read_text())
            self.assertEqual([row["path"] for row in manifest["files"]], ["source/service.stderr.log"])
            details = "".join(traceback.format_exception(caught.exception))
            self.assertIn("missing-source", details)
            self.assertIn("PermissionError: original root-owned lock read error", details)
            self.assertIn("source/service.log.lock", details)

    def test_hosted_folder_failure_keeps_readable_siblings(self) -> None:
        collector = _load_script(
            "dobbyvpn_collect_folder_failure", PRODUCT_ROOT / ".github/scripts/collect_diagnostics.py",
        )
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            source = root / "source"
            blocked = source / "blocked"
            blocked.mkdir(parents=True)
            (source / "test.log").write_bytes(b"readable sibling\n")
            output = root / "diagnostics"
            original_scan = collector.os.scandir

            def scan(path):
                if path == blocked:
                    raise PermissionError("original directory read error")
                return original_scan(path)

            with mock.patch.object(collector.os, "scandir", scan), redirect_stdout(StringIO()), redirect_stderr(BinaryStderr()):
                with self.assertRaises(collector.CollectionError) as caught:
                    collector.collect([source], output)

            self.assertEqual((output / "source/test.log").read_bytes(), b"readable sibling\n")
            self.assertIn("original directory read error", "".join(traceback.format_exception(caught.exception)))

    def test_hosted_write_failure_still_forwards_original_and_copies_other_files(self) -> None:
        collector = _load_script(
            "dobbyvpn_collect_write_failure", PRODUCT_ROOT / ".github/scripts/collect_diagnostics.py",
        )
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            source = root / "source"
            source.mkdir()
            payload = b"original output\x00\xff\n"
            (source / "a.log").write_bytes(payload)
            (source / "b.log").write_bytes(b"other output\n")
            output = root / "diagnostics"
            original_write = Path.write_bytes

            def write(path, data):
                if path == output / "source/a.log":
                    raise OSError("original output write error")
                return original_write(path, data)

            stderr = BinaryStderr()
            with mock.patch.object(Path, "write_bytes", write), redirect_stdout(StringIO()), redirect_stderr(stderr):
                with self.assertRaises(collector.CollectionError) as caught:
                    collector.collect([source], output)

            self.assertIn(payload, stderr.buffer.getvalue())
            self.assertEqual((output / "source/b.log").read_bytes(), b"other output\n")
            self.assertIn("original output write error", "".join(traceback.format_exception(caught.exception)))


if __name__ == "__main__":
    unittest.main()
