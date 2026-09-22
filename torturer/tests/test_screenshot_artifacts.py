from __future__ import annotations

from hashlib import sha256
from pathlib import Path
import struct
import tempfile
import unittest
import zlib

from torturer_checks.screenshot_artifacts import (
    ScreenshotIntegrityError,
    assert_marker_matches,
    png_metadata,
)


def _chunk(kind: bytes, payload: bytes) -> bytes:
    return (
        struct.pack(">I", len(payload))
        + kind
        + payload
        + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
    )


def _png(width: int = 2, height: int = 3) -> bytes:
    signature = b"\x89PNG\r\n\x1a\n"
    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    # Two rows of opaque RGBA pixels; the artifact validator checks framing and
    # CRCs, while the rendered producer supplies the actual screenshot pixels.
    row = b"\x00" + (b"\xff\x00\x00\xff" * width)
    pixels = zlib.compress(row * height)
    return signature + _chunk(b"IHDR", ihdr) + _chunk(b"IDAT", pixels) + _chunk(b"IEND", b"")


class ScreenshotArtifactTests(unittest.TestCase):
    def test_complete_png_metadata_and_marker_are_verified(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "frame.png"
            payload = _png()
            path.write_bytes(payload)
            metadata = png_metadata(path)
            self.assertEqual(metadata["bytes"], len(payload))
            self.assertEqual(metadata["sha256"], sha256(payload).hexdigest())
            self.assertEqual((metadata["width"], metadata["height"]), (2, 3))
            assert_marker_matches(
                metadata,
                bytes_count=len(payload),
                sha256_value=sha256(payload).hexdigest(),
                width=2,
                height=3,
            )

    def test_crc_corruption_and_truncation_are_explicit_failures(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            corrupt = Path(root) / "corrupt.png"
            payload = bytearray(_png())
            payload[-13] ^= 0x01
            corrupt.write_bytes(payload)
            with self.assertRaises(ScreenshotIntegrityError):
                png_metadata(corrupt)

            truncated = Path(root) / "truncated.png"
            truncated.write_bytes(_png()[:-5])
            with self.assertRaises(ScreenshotIntegrityError):
                png_metadata(truncated)


if __name__ == "__main__":
    unittest.main()
