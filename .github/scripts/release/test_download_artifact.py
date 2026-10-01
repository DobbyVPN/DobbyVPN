from __future__ import annotations

import hashlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
import zipfile
from unittest.mock import patch

import download_artifact


class BinaryCapture:
    def __init__(self) -> None:
        self.buffer = io.BytesIO()


class BrokenBinaryOutput:
    class Buffer:
        def write(self, _payload: bytes) -> int:
            raise OSError("stdout unavailable")

        def flush(self) -> None:
            pass

    buffer = Buffer()


def metadata_bytes(digest: str = "sha256:" + "a" * 64) -> bytes:
    return json.dumps(
        [
            {
                "artifacts": [
                    {
                        "id": 47,
                        "name": "selected-artifact",
                        "expired": False,
                        "digest": digest,
                    }
                ]
            }
        ]
    ).encode("utf-8")


class DownloadArtifactTests(unittest.TestCase):
    def test_malformed_metadata_forwards_exact_streams_before_json_error(self) -> None:
        stdout = b'{ "artifacts": [ invalid ] }'
        stderr = b"gh metadata warning\x00\xfe"
        result = subprocess.CompletedProcess(
            ["gh", "api"], 0, stdout=stdout, stderr=stderr
        )
        captured_stdout = BinaryCapture()
        captured_stderr = BinaryCapture()

        with (
            patch.object(download_artifact.subprocess, "run", return_value=result),
            patch.object(download_artifact.sys, "stdout", captured_stdout),
            patch.object(download_artifact.sys, "stderr", captured_stderr),
            self.assertRaises(json.JSONDecodeError),
        ):
            download_artifact.download_artifact(
                "123", "selected-artifact", "/unused", "owner/repo"
            )

        self.assertEqual(captured_stdout.buffer.getvalue(), stdout)
        self.assertEqual(captured_stderr.buffer.getvalue(), stderr)

    def test_nonzero_metadata_forwards_streams_and_reraises_original_error(self) -> None:
        stdout = b'{"message":"not found"}\x00'
        stderr = b"gh request failed\xff"
        error = subprocess.CalledProcessError(
            22, ["gh", "api"], output=stdout, stderr=stderr
        )
        captured_stdout = BinaryCapture()
        captured_stderr = BinaryCapture()

        with (
            patch.object(download_artifact.subprocess, "run", side_effect=error),
            patch.object(download_artifact.sys, "stdout", captured_stdout),
            patch.object(download_artifact.sys, "stderr", captured_stderr),
            self.assertRaises(subprocess.CalledProcessError) as raised,
        ):
            download_artifact.download_artifact(
                "123", "selected-artifact", "/unused", "owner/repo"
            )

        self.assertIs(raised.exception, error)
        self.assertEqual(captured_stdout.buffer.getvalue(), stdout)
        self.assertEqual(captured_stderr.buffer.getvalue(), stderr)

    def test_output_failure_does_not_replace_original_subprocess_error(self) -> None:
        stdout = b"gh response\xff"
        stderr = b"gh failure\xfe"
        error = subprocess.CalledProcessError(
            22, ["gh", "api"], output=stdout, stderr=stderr
        )
        captured_stderr = BinaryCapture()

        with (
            patch.object(download_artifact.subprocess, "run", side_effect=error),
            patch.object(download_artifact.sys, "stdout", BrokenBinaryOutput()),
            patch.object(download_artifact.sys, "stderr", captured_stderr),
            self.assertRaises(subprocess.CalledProcessError) as raised,
        ):
            download_artifact.download_artifact(
                "123", "selected-artifact", "/unused", "owner/repo"
            )

        self.assertIs(raised.exception, error)
        self.assertTrue(
            any("gh output forwarding failed" in note for note in error.__notes__)
        )
        self.assertEqual(captured_stderr.buffer.getvalue(), stderr)

    def test_failed_zip_response_body_is_forwarded_before_archive_closes(self) -> None:
        metadata = metadata_bytes()
        body = b'{"message":"artifact unavailable"}\x00\xff'
        error = subprocess.CalledProcessError(22, ["gh", "api", "artifact.zip"])
        captured_stdout = BinaryCapture()
        captured_stderr = BinaryCapture()
        calls = 0

        def fake_run(command, **kwargs):
            nonlocal calls
            calls += 1
            if calls == 1:
                return subprocess.CompletedProcess(
                    command, 0, stdout=metadata, stderr=b"metadata notice\xfe"
                )
            kwargs["stdout"].write(body)
            raise error

        with (
            patch.object(download_artifact.subprocess, "run", side_effect=fake_run),
            patch.object(download_artifact.sys, "stdout", captured_stdout),
            patch.object(download_artifact.sys, "stderr", captured_stderr),
            self.assertRaises(subprocess.CalledProcessError) as raised,
        ):
            download_artifact.download_artifact(
                "123", "selected-artifact", "/unused", "owner/repo"
            )

        self.assertIs(raised.exception, error)
        self.assertEqual(captured_stdout.buffer.getvalue(), metadata + body)
        self.assertEqual(captured_stderr.buffer.getvalue(), b"metadata notice\xfe")

    def test_success_forwards_metadata_but_not_zip_payload(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary)
            archive_buffer = io.BytesIO()
            with zipfile.ZipFile(archive_buffer, "w") as zipped:
                zipped.writestr("payload.txt", "release payload")
            archive_bytes = archive_buffer.getvalue()
            digest = "sha256:" + hashlib.sha256(archive_bytes).hexdigest()
            metadata = metadata_bytes(digest)
            captured_stdout = BinaryCapture()
            captured_stderr = BinaryCapture()
            calls = 0

            def fake_run(command, **kwargs):
                nonlocal calls
                calls += 1
                if calls == 1:
                    return subprocess.CompletedProcess(
                        command, 0, stdout=metadata, stderr=b"metadata note\xff"
                    )
                kwargs["stdout"].write(archive_bytes)
                return subprocess.CompletedProcess(command, 0)

            with (
                patch.object(download_artifact.subprocess, "run", side_effect=fake_run),
                patch.object(download_artifact.sys, "stdout", captured_stdout),
                patch.object(download_artifact.sys, "stderr", captured_stderr),
            ):
                download_artifact.download_artifact(
                    "123", "selected-artifact", str(destination), "owner/repo"
                )

            self.assertEqual(captured_stdout.buffer.getvalue(), metadata)
            self.assertEqual(captured_stderr.buffer.getvalue(), b"metadata note\xff")
            self.assertEqual(
                (destination / "payload.txt").read_text(encoding="utf-8"),
                "release payload",
            )


if __name__ == "__main__":
    unittest.main()
