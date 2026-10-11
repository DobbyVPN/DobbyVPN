from __future__ import annotations

from hashlib import sha256
from pathlib import Path
import tempfile
import unittest

from PIL import Image

from torturer_runner.screenshot_artifacts import (
    ScreenshotIntegrityError,
    assert_marker_matches,
    file_metadata,
    png_metadata,
)


class ScreenshotArtifactTests(unittest.TestCase):
    def test_decoder_proves_rendered_png_and_keeps_original_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            path = Path(name) / "rendered.png"
            image = Image.new("RGBA", (2, 2))
            image.putdata([
                (255, 0, 0, 255),
                (0, 255, 0, 255),
                (0, 0, 255, 255),
                (255, 255, 255, 255),
            ])
            image.save(path, format="PNG")
            original = path.read_bytes()

            metadata = png_metadata(path)

            self.assertEqual((metadata["width"], metadata["height"]), (2, 2))
            self.assertEqual(metadata["mime"], "image/png")
            self.assertEqual(metadata["bytes"], len(original))
            self.assertEqual(metadata["sha256"], sha256(original).hexdigest())
            self.assertEqual(path.read_bytes(), original)

    def test_transfer_integrity_checks_do_not_decode_png_bytes(self) -> None:
        payload = b"not a decodable PNG, preserved until producer validation"
        with tempfile.TemporaryDirectory() as name:
            path = Path(name) / "capture.png"
            path.write_bytes(payload)

            metadata = file_metadata(path)
            assert_marker_matches(
                metadata,
                bytes_count=len(payload),
                sha256_value=sha256(payload).hexdigest(),
            )

            self.assertEqual(path.read_bytes(), payload)
            with self.assertRaises(ScreenshotIntegrityError):
                assert_marker_matches(
                    metadata,
                    bytes_count=len(payload) + 1,
                    sha256_value=sha256(payload).hexdigest(),
                )

    def test_image_decoder_rejects_truncated_png(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            path = Path(name) / "truncated.png"
            path.write_bytes(b"\x89PNG\r\n\x1a\ntruncated")

            with self.assertRaisesRegex(ScreenshotIntegrityError, "could not be decoded"):
                png_metadata(path)

    def test_image_decoder_rejects_invalid_chunk_checksum(self) -> None:
        with tempfile.TemporaryDirectory() as name:
            path = Path(name) / "bad-crc.png"
            Image.new("RGB", (2, 2), (120, 40, 200)).save(path, format="PNG")
            payload = bytearray(path.read_bytes())
            idat_offset = payload.index(b"IDAT")
            idat_length = int.from_bytes(payload[idat_offset - 4:idat_offset], "big")
            crc_offset = idat_offset + 4 + idat_length
            payload[crc_offset] ^= 1
            path.write_bytes(payload)

            with self.assertRaisesRegex(ScreenshotIntegrityError, "could not be decoded"):
                png_metadata(path)


if __name__ == "__main__":
    unittest.main()
