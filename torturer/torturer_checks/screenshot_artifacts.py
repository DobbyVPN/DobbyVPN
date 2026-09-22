"""Integrity checks for optional rendered UI screenshot artifacts."""

from __future__ import annotations

from hashlib import sha256
from pathlib import Path
import struct
import zlib
from typing import Any


PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


class ScreenshotIntegrityError(ValueError):
    """A screenshot is missing, malformed, or inconsistent with its marker."""


def png_metadata(path: Path) -> dict[str, Any]:
    """Read and validate one complete PNG, returning manifest-ready metadata.

    This validates the complete chunk framing and CRCs, not just the eight-byte
    signature. The caller can compare the returned bytes/hash/dimensions with
    the producer marker before adding the file to the current-run manifest.
    """

    if path.is_symlink() or not path.is_file():
        raise ScreenshotIntegrityError(f"screenshot is not a regular file: {path}")
    try:
        payload = path.read_bytes()
    except OSError as error:
        raise ScreenshotIntegrityError(f"screenshot could not be read: {path}") from error
    if len(payload) < len(PNG_SIGNATURE) + 12 or not payload.startswith(PNG_SIGNATURE):
        raise ScreenshotIntegrityError(f"screenshot PNG signature is invalid: {path}")

    offset = len(PNG_SIGNATURE)
    width: int | None = None
    height: int | None = None
    saw_iend = False
    while offset < len(payload):
        if len(payload) - offset < 12:
            raise ScreenshotIntegrityError(f"screenshot PNG chunk is truncated: {path}")
        length = struct.unpack(">I", payload[offset:offset + 4])[0]
        chunk_type = payload[offset + 4:offset + 8]
        end = offset + 12 + length
        if end > len(payload):
            raise ScreenshotIntegrityError(f"screenshot PNG chunk exceeds file: {path}")
        chunk_data = payload[offset + 8:offset + 8 + length]
        expected_crc = struct.unpack(">I", payload[offset + 8 + length:end])[0]
        if zlib.crc32(chunk_type + chunk_data) & 0xFFFFFFFF != expected_crc:
            raise ScreenshotIntegrityError(f"screenshot PNG CRC is invalid: {path}")
        if chunk_type == b"IHDR":
            if offset != len(PNG_SIGNATURE) or length != 13:
                raise ScreenshotIntegrityError(f"screenshot PNG IHDR is invalid: {path}")
            width, height = struct.unpack(">II", chunk_data[:8])
            if width <= 0 or height <= 0:
                raise ScreenshotIntegrityError(f"screenshot PNG dimensions are invalid: {path}")
        elif chunk_type == b"IEND":
            if length != 0 or width is None or height is None:
                raise ScreenshotIntegrityError(f"screenshot PNG IEND is invalid: {path}")
            saw_iend = True
            if end != len(payload):
                raise ScreenshotIntegrityError(f"screenshot PNG has trailing bytes: {path}")
            break
        offset = end
    if not saw_iend or width is None or height is None:
        raise ScreenshotIntegrityError(f"screenshot PNG has no complete IEND: {path}")
    return {
        "path": str(path),
        "mime": "image/png",
        "bytes": len(payload),
        "sha256": sha256(payload).hexdigest(),
        "width": width,
        "height": height,
    }


def assert_marker_matches(
    metadata: dict[str, Any],
    *,
    bytes_count: int,
    sha256_value: str,
    width: int,
    height: int,
) -> None:
    """Reject a pulled artifact whose producer metadata does not match it."""

    expected = {
        "bytes": bytes_count,
        "sha256": sha256_value,
        "width": width,
        "height": height,
    }
    observed = {key: metadata.get(key) for key in expected}
    if observed != expected:
        raise ScreenshotIntegrityError(
            f"screenshot marker mismatch: expected={expected!r} observed={observed!r}"
        )


__all__ = ["ScreenshotIntegrityError", "assert_marker_matches", "png_metadata"]
