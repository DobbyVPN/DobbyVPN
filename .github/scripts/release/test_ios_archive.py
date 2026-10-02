from __future__ import annotations

import binascii
import plistlib
from pathlib import Path
import random
import stat
import struct
import sys
import tempfile
import unittest
import zipfile

IOS_SCRIPT_DIR = Path(__file__).resolve().parents[1] / "ios"
sys.path.insert(0, str(IOS_SCRIPT_DIR))
import ios_archive

SOURCE_SHA = "a" * 40
VERSION = "1.2.3"
BUILD_NUMBER = "1002003"
IPA_COMMENT = b"synthetic signed IPA"


def _extra(field_id: int, payload: bytes) -> bytes:
    return struct.pack("<HH", field_id, len(payload)) + payload


def _extra_ids(extra: bytes) -> set[int]:
    result = set()
    offset = 0
    while offset < len(extra):
        field_id, length = struct.unpack_from("<HH", extra, offset)
        result.add(field_id)
        offset += 4 + length
    return result


def _write_signed_ipa(path: Path) -> dict[str, bytes]:
    app_root = "Payload/DobbyVPN.app"
    tunnel_root = f"{app_root}/PlugIns/DobbyVPNTunnel.appex"
    app_info = plistlib.dumps(
        {
            "CFBundleIdentifier": ios_archive.APP_BUNDLE_ID,
            "CFBundleShortVersionString": VERSION,
            "CFBundleVersion": BUILD_NUMBER,
            "DobbySourceCommit": SOURCE_SHA,
        }
    )
    tunnel_info = plistlib.dumps(
        {
            "CFBundleIdentifier": ios_archive.TUNNEL_BUNDLE_ID,
            "DobbySourceCommit": SOURCE_SHA,
            "NSExtension": {
                "NSExtensionPointIdentifier": ios_archive.TUNNEL_POINT,
            },
        }
    )
    words = [f"word{index:03d}" for index in range(256)]
    randomizer = random.Random(12345)
    localized_resource = " ".join(
        randomizer.choice(words) for _ in range(30_000)
    ).encode()
    entries = {
        f"{app_root}/Info.plist": app_info,
        f"{app_root}/DobbyVPN": b"synthetic signed executable",
        f"{app_root}/_CodeSignature/CodeResources": b"synthetic code resource seal",
        f"{app_root}/archived-expanded-entitlements.xcent": b"synthetic app entitlements",
        f"{app_root}/Resources/en.lproj/Localizable.strings": localized_resource,
        f"{tunnel_root}/Info.plist": tunnel_info,
        f"{tunnel_root}/Tunnel": b"synthetic signed tunnel executable",
        f"{tunnel_root}/archived-expanded-entitlements.xcent": b"synthetic tunnel entitlements",
        f"{app_root}/Frameworks/Current": b"Versions/A/Current",
    }
    symlink = f"{app_root}/Frameworks/Current"
    unicode_path = (
        b"\x01"
        + struct.pack("<I", binascii.crc32(symlink.encode()))
        + symlink.encode()
    )
    benign_timestamp = b"\x01" + struct.pack("<I", 1_700_000_000)
    with zipfile.ZipFile(
        path,
        mode="w",
        compression=zipfile.ZIP_DEFLATED,
        compresslevel=1,
    ) as archive:
        archive.comment = IPA_COMMENT
        for name, data in entries.items():
            info = zipfile.ZipInfo(name, date_time=(2026, 10, 2, 12, 0, 0))
            info.create_system = 3
            mode = stat.S_IFLNK | 0o777 if name == symlink else stat.S_IFREG | 0o644
            if name.endswith("/DobbyVPN") or name.endswith("/Tunnel"):
                mode = stat.S_IFREG | 0o755
            info.external_attr = mode << 16
            if name == symlink:
                info.extra = b"".join(
                    (
                        _extra(0x5455, benign_timestamp),
                        _extra(0x0001, struct.pack("<QQ", len(data), len(data))),
                        _extra(0x7075, unicode_path),
                    )
                )
            archive.writestr(
                info,
                data,
                compress_type=zipfile.ZIP_DEFLATED,
                compresslevel=1,
            )
            if name.endswith("Localizable.strings"):
                info.external_attr = 0
    return entries


class IOSArchiveCompressionTests(unittest.TestCase):
    def test_compress_ipa_preserves_payload_metadata_and_provenance(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            ipa = Path(temporary) / "DobbyVPN.ipa"
            expected_entries = _write_signed_ipa(ipa)
            original_size = ipa.stat().st_size
            with zipfile.ZipFile(ipa) as archive:
                original_attributes = {
                    info.filename: info.external_attr for info in archive.infolist()
                }

            ios_archive.compress_ipa(ipa)

            with zipfile.ZipFile(ipa) as archive:
                self.assertEqual(archive.comment, IPA_COMMENT)
                self.assertIsNone(archive.testzip())
                self.assertLess(ipa.stat().st_size, original_size)
                self.assertEqual(
                    set(archive.namelist()), set(expected_entries)
                )
                for info in archive.infolist():
                    self.assertEqual(info.compress_type, zipfile.ZIP_DEFLATED)
                    self.assertEqual(archive.read(info), expected_entries[info.filename])
                    self.assertEqual(info.external_attr, original_attributes[info.filename])
                executable = archive.getinfo("Payload/DobbyVPN.app/DobbyVPN")
                self.assertEqual(
                    stat.S_IMODE(executable.external_attr >> 16), 0o755
                )
                symlink = archive.getinfo("Payload/DobbyVPN.app/Frameworks/Current")
                self.assertEqual(
                    stat.S_IFMT(symlink.external_attr >> 16), stat.S_IFLNK
                )
                self.assertEqual(stat.S_IMODE(symlink.external_attr >> 16), 0o777)
                self.assertEqual(_extra_ids(symlink.extra), {0x5455, 0x7075})

            ios_archive.verify_ipa(
                ipa,
                source_sha=SOURCE_SHA,
                version=VERSION,
                build_number=BUILD_NUMBER,
            )
            with self.assertRaises(ios_archive.ArchiveError):
                ios_archive.verify_ipa(
                    ipa,
                    source_sha="b" * 40,
                    version=VERSION,
                    build_number=BUILD_NUMBER,
                )

    def test_corrupt_ipa_keeps_original_and_removes_temporary_file(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            ipa = Path(temporary) / "corrupt.ipa"
            with zipfile.ZipFile(ipa, mode="w", compression=zipfile.ZIP_STORED) as archive:
                archive.writestr("payload.bin", b"payload bytes to corrupt")
            with zipfile.ZipFile(ipa) as archive:
                info = archive.getinfo("payload.bin")
            with ipa.open("r+b") as stream:
                stream.seek(info.header_offset)
                header = stream.read(30)
                name_length, extra_length = struct.unpack_from("<HH", header, 26)
                data_offset = info.header_offset + 30 + name_length + extra_length
                stream.seek(data_offset)
                first_byte = stream.read(1)
                stream.seek(data_offset)
                stream.write(bytes([first_byte[0] ^ 0x01]))
            original_bytes = ipa.read_bytes()
            temporary_ipa = ipa.with_name(f".{ipa.name}.compressed.tmp")

            with self.assertRaises(zipfile.BadZipFile):
                ios_archive.compress_ipa(ipa)

            self.assertEqual(ipa.read_bytes(), original_bytes)
            self.assertFalse(temporary_ipa.exists())


if __name__ == "__main__":
    unittest.main()
