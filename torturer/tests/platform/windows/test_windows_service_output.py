from __future__ import annotations

from contextlib import redirect_stderr
from io import BytesIO
from types import SimpleNamespace
import unittest

from torturer_runner.adapters.windows import WindowsServiceProcessController


class BinaryStderr:
    def __init__(self) -> None:
        self.buffer = BytesIO()


class WindowsServiceOutputTests(unittest.TestCase):
    @staticmethod
    def controller() -> WindowsServiceProcessController:
        controller = WindowsServiceProcessController.__new__(
            WindowsServiceProcessController
        )
        controller._service_diagnostics = []
        controller._replacement_stream_threads = []
        return controller

    def test_forwards_exact_bytes_and_marks_each_stream(self) -> None:
        controller = self.controller()
        forwarded = BinaryStderr()
        stdout = b"out line\x00\xff\npartial stdout\xfe"
        stderr = b"err line\x00\xfd\npartial stderr\xfc"

        with redirect_stderr(forwarded):
            controller._forward_replacement_output(
                SimpleNamespace(stdout=BytesIO(stdout), stderr=None)
            )
            controller._join_replacement_output()
            controller._forward_replacement_output(
                SimpleNamespace(stdout=None, stderr=BytesIO(stderr))
            )
            controller._join_replacement_output()

        self.assertEqual(
            forwarded.buffer.getvalue(),
            b"[windows-service stdout]\n" + stdout
            + b"[windows-service stderr]\n" + stderr,
        )
        self.assertEqual(controller._service_diagnostics, [])

    def test_records_forwarding_typeerror_and_keeps_draining_pipe(self) -> None:
        class FailingBuffer:
            def write(self, _payload: bytes) -> int:
                raise TypeError("binary sink rejected output")

            def flush(self) -> None:
                pass

        class TrackedPipe(BytesIO):
            reached_eof = False

            def readline(self, *args: object, **kwargs: object) -> bytes:
                chunk = super().readline(*args, **kwargs)
                if not chunk:
                    self.reached_eof = True
                return chunk

        controller = self.controller()
        pipe = TrackedPipe(b"child output\npartial")
        destination = SimpleNamespace(buffer=FailingBuffer())

        with redirect_stderr(destination):
            controller._forward_replacement_output(
                SimpleNamespace(stdout=pipe, stderr=None)
            )
            controller._join_replacement_output()

        self.assertTrue(pipe.reached_eof)
        self.assertTrue(
            any(
                "stream=stdout error=TypeError detail=binary sink rejected output" in note
                for note in controller._service_diagnostics
            )
        )


if __name__ == "__main__":
    unittest.main()
