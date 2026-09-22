from __future__ import annotations

import hashlib
import io
import json
from pathlib import Path
import struct
import tempfile
import unittest
import zlib
from contextlib import redirect_stderr, redirect_stdout

import collect_diagnostics


def png(width: int = 2, height: int = 1, extra: bytes = b"") -> bytes:
    signature = b"\x89PNG\r\n\x1a\n"
    def chunk(kind: bytes, value: bytes) -> bytes:
        return struct.pack(">I", len(value)) + kind + value + struct.pack(">I", zlib.crc32(kind + value) & 0xffffffff)
    raw = b"\x00" + b"\xff\x00\x00" * width
    return signature + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) + chunk(b"tEXt", extra) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


class CollectDiagnosticsTests(unittest.TestCase):
    def test_forwards_every_text_payload_and_only_metadata_for_binary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text('Password = "secret-value"\n', encoding="utf-8")
            source = root / "run"
            source.mkdir()
            (source / "diagnostic.log").write_bytes(
                b"public context secret-value malformed=\xff\n"
            )
            (source / "opaque.dat").write_bytes(b"\x00secret-value\xff")
            output = root / "collected"
            forwarded = io.StringIO()
            metadata = io.StringIO()
            with redirect_stderr(forwarded), redirect_stdout(metadata):
                records = collect_diagnostics.collect(profile, [source], output)

            text = forwarded.getvalue()
            self.assertIn("[diagnostic path=run/diagnostic.log stream=text begin]", text)
            self.assertIn("[diagnostic path=run/diagnostic.log stream=text end]", text)
            self.assertIn("public context", text)
            self.assertIn("malformed=\\xff", text)
            self.assertNotIn("secret-value", text)
            self.assertNotIn("opaque.dat", text)
            self.assertIn('"path": "run/opaque.dat"', metadata.getvalue())
            opaque = next(record for record in records if record["path"] == "run/opaque.dat")
            self.assertEqual(opaque["kind"], "binary")
            self.assertEqual((output / "run" / "opaque.dat").read_bytes(), b"\x00secret-value\xff")

    def test_redacts_only_private_text_and_preserves_png_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text(
                'enabled = true\n[[Outline]]\nDescription = "public-label"\nServer = "private.example"\nPassword = "secret-value"\nToken = "x"\n',
                encoding="utf-8",
            )
            source = root / "run"
            source.mkdir()
            (source / "result.json").write_text(
                '{"enabled":true,"description":"public-label","server":"private.example","password":"secret-value","token":"x","context":"x"}\n',
                encoding="utf-8",
            )
            image = png(extra=b"secret-value private.example true public-label")
            (source / "screen.png").write_bytes(image)
            output = root / "collected"
            records = collect_diagnostics.collect(profile, [source], output)
            text = (output / "run" / "result.json").read_text(encoding="utf-8")
            self.assertIn('"enabled":true', text)
            self.assertIn("public-label", text)
            self.assertNotIn("private.example", text)
            self.assertNotIn("secret-value", text)
            self.assertIn('"token":"[REDACTED]"', text)
            self.assertIn('"context":"x"', text)
            self.assertEqual((output / "run" / "screen.png").read_bytes(), image)
            png_record = next(record for record in records if record["path"] == "run/screen.png")
            self.assertEqual(png_record["sha256"], hashlib.sha256(image).hexdigest())
            self.assertEqual((png_record["width"], png_record["height"]), (2, 1))
            manifest = json.loads((output / "manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(manifest["schema"], 2)
            self.assertEqual(manifest["files"][0]["mime_type"], "application/json")

    def test_rejects_invalid_png_without_truncating_it(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text("[[Outline]]\nPassword='secret-value'\n", encoding="utf-8")
            source = root / "run"
            source.mkdir()
            (source / "bad.png").write_bytes(b"not-png-secret-value")
            with self.assertRaises(collect_diagnostics.CollectionError):
                collect_diagnostics.collect(profile, [source], root / "collected")

    def test_rejects_corrupt_png_crc_and_truncated_iend(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_text("Password='secret-value'\n", encoding="utf-8")
            for name, payload in (
                ("bad-crc.png", bytearray(png())),
                ("truncated.png", png()[:-8]),
            ):
                if name == "bad-crc.png":
                    payload[-1] ^= 0x01
                source = root / name
                source.mkdir()
                (source / "capture.png").write_bytes(bytes(payload))
                with self.subTest(name=name), self.assertRaises(collect_diagnostics.CollectionError):
                    collect_diagnostics.collect(profile, [source], root / f"collected-{name}")

    def test_malformed_profile_extracts_contextual_short_private_values(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            profile = root / "profile.toml"
            profile.write_bytes(b"Password = 'x'\nunterminated = [\nToken: zz\n")
            values = collect_diagnostics._private_values(profile)
            self.assertIn(b"x", values)
            self.assertIn(b"zz", values)
            self.assertEqual(
                collect_diagnostics._redact(
                    b'password="x" token: zz context=x', values,
                ),
                b'password="[REDACTED]" token: [REDACTED] context=x',
            )


if __name__ == "__main__":
    unittest.main()
