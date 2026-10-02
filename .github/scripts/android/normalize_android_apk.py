#!/usr/bin/env python3
"""Recompress an unsigned APK deterministically before pinned zipalign."""

from __future__ import annotations

import argparse
import copy
import os
from pathlib import Path
import stat
import struct
import subprocess
import tempfile
import zipfile

from android_dependency_provenance import ANDROID_BUILD_TOOLS
from verify_android_reproducibility import verify_signed_payload


ZIP64_EXTRA_ID = 0x0001
ZIP_METHODS = {zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED}


def _without_zip64_extra(extra: bytes) -> bytes:
    kept = bytearray()
    offset = 0
    while offset < len(extra):
        if len(extra) - offset < 4:
            raise ValueError("APK contains a truncated ZIP extra field")
        field_id, field_size = struct.unpack_from("<HH", extra, offset)
        end = offset + 4 + field_size
        if end > len(extra):
            raise ValueError("APK contains a truncated ZIP extra field")
        if field_id != ZIP64_EXTRA_ID:
            kept.extend(extra[offset:end])
        offset = end
    return bytes(kept)


def _temporary_apk(directory: Path, name: str) -> Path:
    descriptor, filename = tempfile.mkstemp(
        prefix=f".{name}.", suffix=".apk", dir=directory
    )
    os.close(descriptor)
    return Path(filename)


def _semantic_metadata(apk: Path) -> tuple[bytes, list[tuple[object, ...]]]:
    with zipfile.ZipFile(apk) as archive:
        entries = []
        for item in archive.infolist():
            if item.compress_type not in ZIP_METHODS:
                raise ValueError(
                    f"unsupported APK ZIP method {item.compress_type} for {item.filename}"
                )
            entries.append(
                (
                    item.filename,
                    item.compress_type,
                    item.date_time,
                    item.comment,
                    item.create_system,
                    item.internal_attr,
                    item.external_attr,
                )
            )
        return archive.comment, entries


def _write_level_nine(source: Path, destination: Path) -> None:
    with zipfile.ZipFile(source, "r") as original, zipfile.ZipFile(
        destination, "w", allowZip64=True
    ) as normalized:
        normalized.comment = original.comment
        for old_info in original.infolist():
            if old_info.compress_type not in ZIP_METHODS:
                raise ValueError(
                    f"unsupported APK ZIP method {old_info.compress_type} for {old_info.filename}"
                )
            info = copy.copy(old_info)
            info.extra = _without_zip64_extra(info.extra)
            payload = original.read(old_info)
            if info.compress_type == zipfile.ZIP_DEFLATED:
                normalized.writestr(
                    info,
                    payload,
                    compress_type=info.compress_type,
                    compresslevel=9,
                )
            else:
                normalized.writestr(
                    info, payload, compress_type=info.compress_type
                )
            # zipfile assigns default Unix permissions when external_attr is
            # zero; restore the source value for the central directory.
            info.external_attr = old_info.external_attr


def _zipalign_for_sdk(sdk_root: Path) -> Path:
    candidate = sdk_root / "build-tools" / ANDROID_BUILD_TOOLS / "zipalign"
    if not candidate.is_file() or not os.access(candidate, os.X_OK):
        raise FileNotFoundError(
            f"pinned Android zipalign {ANDROID_BUILD_TOOLS} is unavailable: {candidate}"
        )
    return candidate


def normalize_apk(apk: Path, zipalign: Path) -> None:
    apk = apk.resolve(strict=True)
    if not apk.is_file():
        raise FileNotFoundError(f"unsigned APK is unavailable: {apk}")
    if not zipalign.is_file() or not os.access(zipalign, os.X_OK):
        raise FileNotFoundError(f"zipalign is unavailable or not executable: {zipalign}")

    expected_metadata = _semantic_metadata(apk)
    canonical = _temporary_apk(apk.parent, apk.name)
    aligned = _temporary_apk(apk.parent, apk.name)
    try:
        _write_level_nine(apk, canonical)
        # zipalign writes to a new output path; leave the original APK intact
        # until payload and compression checks have passed.
        aligned.unlink()
        subprocess.run(
            [str(zipalign), "-P", "16", "-f", "-v", "4", str(canonical), str(aligned)],
            check=True,
        )
        subprocess.run(
            [str(zipalign), "-c", "-P", "16", "-v", "4", str(aligned)],
            check=True,
        )
        verify_signed_payload(apk, aligned)
        if _semantic_metadata(aligned) != expected_metadata:
            raise ValueError("APK ZIP entry methods or metadata changed during normalization")

        os.chmod(aligned, stat.S_IMODE(apk.stat().st_mode))
        os.replace(aligned, apk)
    finally:
        canonical.unlink(missing_ok=True)
        aligned.unlink(missing_ok=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apk", type=Path, required=True)
    parser.add_argument("--sdk-root", type=Path, required=True)
    args = parser.parse_args()
    normalize_apk(args.apk, _zipalign_for_sdk(args.sdk_root.resolve(strict=True)))


if __name__ == "__main__":
    main()
