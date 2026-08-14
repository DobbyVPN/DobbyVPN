#!/usr/bin/env python3
from __future__ import annotations

import copy
import importlib.util
from pathlib import Path
import tempfile
import unittest
import zipfile


SCRIPT = Path(__file__).with_name("verify_android_reproducibility.py")
SPEC = importlib.util.spec_from_file_location("android_repro", SCRIPT)
assert SPEC and SPEC.loader
REPRO = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REPRO)


SOURCE_SHA = "a" * 40
VERSION_NAME = "1.4.8"
VERSION_CODE = 1_004_008


def write_apk(path: Path, suffix: bytes = b"", signature: bytes | None = None) -> None:
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_STORED) as archive:
        archive.writestr("AndroidManifest.xml", b"manifest" + suffix)
        archive.writestr("lib/arm64-v8a/libgojni.so", b"arm64-library")
        archive.writestr("lib/x86_64/libgojni.so", b"x86-library")
        if signature is not None:
            archive.writestr("META-INF/DOBBYVPN.SF", b"signature manifest")
            archive.writestr("META-INF/DOBBYVPN.RSA", signature)


class AndroidReproducibilityTests(unittest.TestCase):
    def test_identical_complete_apks_create_and_verify(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = root / "first.apk"
            second = root / "second.apk"
            write_apk(first)
            second.write_bytes(first.read_bytes())
            document = REPRO.create_document(
                first, second, SOURCE_SHA, VERSION_NAME, VERSION_CODE
            )
            self.assertTrue(document["identical"])
            self.assertEqual(document["builds"][0], document["builds"][1] | {"id": "first"})
            REPRO.verify_document(document, second, SOURCE_SHA, VERSION_NAME, VERSION_CODE)

    def test_non_native_apk_difference_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = root / "first.apk"
            second = root / "second.apk"
            write_apk(first, b"-first")
            write_apk(second, b"-second")
            with self.assertRaisesRegex(REPRO.VerificationError, "not byte-identical"):
                REPRO.create_document(first, second, SOURCE_SHA, VERSION_NAME, VERSION_CODE)

    def test_missing_native_library_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            apk = Path(directory) / "bad.apk"
            with zipfile.ZipFile(apk, "w") as archive:
                archive.writestr("lib/arm64-v8a/libgojni.so", b"arm64")
            with self.assertRaisesRegex(REPRO.VerificationError, "x86_64"):
                REPRO.create_document(apk, apk, SOURCE_SHA, VERSION_NAME, VERSION_CODE)

    def test_tampered_provenance_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            apk = root / "app.apk"
            write_apk(apk)
            document = REPRO.create_document(
                apk, apk, SOURCE_SHA, VERSION_NAME, VERSION_CODE
            )
            tampered = copy.deepcopy(document)
            tampered["builds"][0]["sha256"] = "0" * 64
            with self.assertRaisesRegex(REPRO.VerificationError, "digest or size"):
                REPRO.verify_document(tampered, apk, SOURCE_SHA, VERSION_NAME, VERSION_CODE)

    def test_signed_payload_may_add_only_signature_members(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            unsigned = root / "unsigned.apk"
            signed = root / "signed.apk"
            write_apk(unsigned)
            write_apk(signed, signature=b"signature bytes")
            REPRO.verify_signed_payload(unsigned, signed)

    def test_signed_payload_change_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            unsigned = root / "unsigned.apk"
            signed = root / "signed.apk"
            write_apk(unsigned)
            write_apk(signed, suffix=b"-changed", signature=b"signature bytes")
            with self.assertRaisesRegex(REPRO.VerificationError, "payload differs"):
                REPRO.verify_signed_payload(unsigned, signed)

    def test_nested_meta_inf_payload_is_not_treated_as_a_signature(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            unsigned = root / "unsigned.apk"
            signed = root / "signed.apk"
            write_apk(unsigned)
            write_apk(signed)
            with zipfile.ZipFile(signed, "a") as archive:
                archive.writestr("META-INF/runtime/library.RSA", b"application payload")
            with self.assertRaisesRegex(REPRO.VerificationError, "payload differs"):
                REPRO.verify_signed_payload(unsigned, signed)

    def test_duplicate_zip_member_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            apk = Path(directory) / "duplicate.apk"
            with zipfile.ZipFile(apk, "w") as archive:
                archive.writestr("lib/arm64-v8a/libgojni.so", b"first")
                archive.writestr("lib/arm64-v8a/libgojni.so", b"second")
                archive.writestr("lib/x86_64/libgojni.so", b"x86")
            with self.assertRaisesRegex(REPRO.VerificationError, "duplicate"):
                REPRO.create_document(apk, apk, SOURCE_SHA, VERSION_NAME, VERSION_CODE)


if __name__ == "__main__":
    unittest.main()
