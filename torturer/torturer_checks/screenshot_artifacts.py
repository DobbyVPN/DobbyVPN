"""Image and byte-integrity helpers for test screenshots."""

from __future__ import annotations

from hashlib import sha256
from pathlib import Path
import re
from typing import Any


MAX_SCREENSHOT_PIXELS = 64 * 1024 * 1024
_SHA256 = re.compile(r"[0-9a-f]{64}\Z")


class ScreenshotIntegrityError(ValueError):
    """A screenshot is missing, malformed, or inconsistent with its marker."""


def _regular_file(path: Path) -> None:
    if path.is_symlink() or not path.is_file():
        raise ScreenshotIntegrityError(f"screenshot is not a regular file: {path}")


def file_metadata(path: Path) -> dict[str, Any]:
    """Return exact file size and SHA-256 without decoding or rewriting it."""

    _regular_file(path)
    digest = sha256()
    size = 0
    try:
        with path.open("rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
                size += len(block)
    except OSError as error:
        raise ScreenshotIntegrityError(f"screenshot could not be read: {path}") from error
    return {"path": str(path), "bytes": size, "sha256": digest.hexdigest()}


def assert_files_identical(source: Path, destination: Path) -> None:
    """Prove a screenshot copy retained the producer's original bytes."""

    expected = file_metadata(source)
    observed = file_metadata(destination)
    if (observed["bytes"], observed["sha256"]) != (
        expected["bytes"], expected["sha256"],
    ):
        raise ScreenshotIntegrityError(
            "screenshot copy changed bytes: "
            f"source={source} destination={destination}"
        )


def _decode_png(path: Path):
    """Decode one PNG with Pillow and return its loaded pixels."""

    _regular_file(path)
    try:
        from PIL import Image
    except ImportError as error:
        raise ScreenshotIntegrityError("Pillow is required to validate screenshots") from error

    try:
        with Image.open(path) as probe:
            if probe.format != "PNG":
                raise ScreenshotIntegrityError(f"screenshot {path} is not a PNG")
            width, height = probe.size
            if width <= 0 or height <= 0 or width * height > MAX_SCREENSHOT_PIXELS:
                raise ScreenshotIntegrityError(f"screenshot {path} dimensions are invalid")
            probe.verify()
        with Image.open(path) as image:
            if image.format != "PNG":
                raise ScreenshotIntegrityError(f"screenshot {path} is not a PNG")
            image.load()
            return image.copy()
    except ScreenshotIntegrityError:
        raise
    except (OSError, ValueError, SyntaxError) as error:
        raise ScreenshotIntegrityError(
            f"screenshot {path} could not be decoded as a complete PNG"
        ) from error


def png_metadata(path: Path) -> dict[str, Any]:
    """Fully decode one produced PNG and return manifest-ready metadata."""

    image = _decode_png(path)
    metadata = file_metadata(path)
    metadata.update({
        "mime": "image/png",
        "width": image.width,
        "height": image.height,
    })
    return metadata


def nonblank_png_dimensions(path: Path) -> tuple[int, int]:
    """Decode and require visible, nonuniform pixels in a rendered screenshot."""

    image = _decode_png(path)
    rgb = image.convert("RGB")
    extrema = rgb.getextrema()
    if all(low == 0 and high == 0 for low, high in extrema):
        raise ScreenshotIntegrityError(f"screenshot {path} is blank")
    if all(low == high for low, high in extrema):
        raise ScreenshotIntegrityError(f"screenshot {path} is uniformly blank")
    if image.convert("RGBA").getchannel("A").getextrema()[1] == 0:
        raise ScreenshotIntegrityError(f"screenshot {path} has no visible pixels")
    return image.width, image.height


def assert_marker_matches(
    metadata: dict[str, Any],
    *,
    bytes_count: int,
    sha256_value: str,
) -> None:
    """Compare a pulled screenshot to producer size and digest metadata."""

    expected = {"bytes": bytes_count, "sha256": sha256_value}
    observed = {key: metadata.get(key) for key in expected}
    if (
        type(bytes_count) is not int
        or bytes_count <= 0
        or not isinstance(sha256_value, str)
        or _SHA256.fullmatch(sha256_value) is None
        or observed != expected
    ):
        raise ScreenshotIntegrityError(
            f"screenshot marker mismatch: expected={expected!r} observed={observed!r}"
        )


__all__ = [
    "MAX_SCREENSHOT_PIXELS",
    "ScreenshotIntegrityError",
    "assert_files_identical",
    "assert_marker_matches",
    "file_metadata",
    "nonblank_png_dimensions",
    "png_metadata",
]
