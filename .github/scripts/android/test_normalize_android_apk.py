from __future__ import annotations

import hashlib
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zipfile
import zlib

from normalize_android_apk import normalize_apk
from verify_android_reproducibility import verify_signed_payload


def _write_fake_zipalign(path: Path, *, fail: bool = False) -> None:
    behavior = "raise SystemExit(9)" if fail else "shutil.copyfile(args[-2], args[-1])"
    path.write_text(
        "#!/usr/bin/env python3\n"
        "import shutil, sys\n"
        "args = sys.argv[1:]\n"
        "if args[0] == '-c':\n"
        "    raise SystemExit(0)\n"
        f"{behavior}\n",
        encoding="utf-8",
    )
    path.chmod(0o755)


def _write_source_apk(path: Path) -> None:
    shared_extra = b"\x34\x12\x04\x00keep"
    zip64_extra = b"\x01\x00\x10\x00" + (1).to_bytes(8, "little") + (2).to_bytes(8, "little")
    stored = zipfile.ZipInfo("assets/stored.bin", (2024, 2, 3, 4, 5, 6))
    stored.compress_type = zipfile.ZIP_STORED
    stored.create_system = 3
    stored.internal_attr = 1
    stored.external_attr = 0o100644 << 16
    stored.comment = b"entry comment"
    stored.extra = shared_extra + zip64_extra

    compressed = zipfile.ZipInfo("classes.dex", (2024, 2, 3, 4, 5, 6))
    compressed.compress_type = zipfile.ZIP_DEFLATED
    compressed.create_system = 3
    compressed.internal_attr = 2
    compressed.external_attr = 0
    compressed.comment = b"dex comment"
    compressed.extra = shared_extra
    payload = (b"classes and resources " * 12000) + bytes(range(256))

    with zipfile.ZipFile(path, "w") as archive:
        archive.comment = b"archive comment"
        archive.writestr(stored, b"stored payload")
        archive.writestr(compressed, payload, compresslevel=1)
        archive.filelist[-1].external_attr = 0


def _raw_compressed_data(apk: Path, info: zipfile.ZipInfo) -> bytes:
    with apk.open("rb") as stream:
        stream.seek(info.header_offset)
        header = stream.read(30)
        filename_size = int.from_bytes(header[26:28], "little")
        extra_size = int.from_bytes(header[28:30], "little")
        stream.seek(info.header_offset + 30 + filename_size + extra_size)
        return stream.read(info.compress_size)


class AndroidApkNormalizationTests(unittest.TestCase):
    def test_level_nine_preserves_payload_methods_and_entry_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            apk = directory / "candidate.apk"
            original = directory / "original.apk"
            zipalign = directory / "zipalign"
            _write_source_apk(apk)
            shutil.copyfile(apk, original)
            _write_fake_zipalign(zipalign)

            normalize_apk(apk, zipalign)

            with zipfile.ZipFile(apk) as archive:
                self.assertEqual(archive.comment, b"archive comment")
                stored, compressed = archive.infolist()
                self.assertEqual(stored.compress_type, zipfile.ZIP_STORED)
                self.assertEqual(compressed.compress_type, zipfile.ZIP_DEFLATED)
                self.assertEqual(stored.date_time, (2024, 2, 3, 4, 5, 6))
                self.assertEqual(compressed.date_time, (2024, 2, 3, 4, 5, 6))
                self.assertEqual(stored.comment, b"entry comment")
                self.assertEqual(compressed.comment, b"dex comment")
                self.assertEqual(stored.extra, b"\x34\x12\x04\x00keep")
                self.assertEqual(compressed.extra, b"\x34\x12\x04\x00keep")
                self.assertEqual(stored.create_system, 3)
                self.assertEqual(stored.internal_attr, 1)
                self.assertEqual(compressed.internal_attr, 2)
                self.assertEqual(stored.external_attr, 0o100644 << 16)
                self.assertEqual(compressed.external_attr, 0)
                payload = archive.read(compressed)
                compressor = zlib.compressobj(9, zlib.DEFLATED, -15)
                expected = compressor.compress(payload) + compressor.flush()
                self.assertEqual(_raw_compressed_data(apk, compressed), expected)

            verify_signed_payload(original, apk)

    def test_zipalign_failure_leaves_original_apk_untouched(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            apk = directory / "candidate.apk"
            zipalign = directory / "zipalign"
            _write_source_apk(apk)
            _write_fake_zipalign(zipalign, fail=True)
            original_hash = hashlib.sha256(apk.read_bytes()).hexdigest()

            with self.assertRaises(subprocess.CalledProcessError):
                normalize_apk(apk, zipalign)

            self.assertEqual(hashlib.sha256(apk.read_bytes()).hexdigest(), original_hash)
            self.assertEqual(
                sorted(path.name for path in directory.iterdir()),
                ["candidate.apk", "zipalign"],
            )


if __name__ == "__main__":
    unittest.main()
