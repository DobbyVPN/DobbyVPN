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
from unittest import mock
import zipfile

IOS_SCRIPT_DIR = Path(__file__).resolve().parents[1] / "ios"
sys.path.insert(0, str(IOS_SCRIPT_DIR))
import ios_archive
import verify_ios_app_group

SOURCE_SHA = "a" * 40
VERSION = "1.2.3"
BUILD_NUMBER = "1002003"
IPA_COMMENT = b"synthetic signed IPA"
TEAM_ID = "ABCDEFGHIJ"
KEYCHAIN_GROUP_TEMPLATE = "$(AppIdentifierPrefix)vpn.dobby.app"


def _write_unsigned_archive(
    archive: Path,
    *,
    app_bundle_id: str = ios_archive.APP_BUNDLE_ID,
    keychain_group: str = "vpn.dobby.app",
) -> tuple[Path, Path]:
    app = archive / "Products" / "Applications" / "DobbyVPN.app"
    tunnel = app / "PlugIns" / "DobbyVPNTunnel.appex"
    app.mkdir(parents=True)
    tunnel.mkdir(parents=True)
    (app / "DobbyVPN").write_bytes(b"synthetic app executable")
    (app / "DobbyVPN").chmod(0o755)
    (tunnel / "Tunnel").write_bytes(b"synthetic tunnel executable")
    (tunnel / "Tunnel").chmod(0o755)
    (app / "Info.plist").write_bytes(
        plistlib.dumps(
            {
                "CFBundleIdentifier": app_bundle_id,
                "CFBundleShortVersionString": VERSION,
                "CFBundleVersion": BUILD_NUMBER,
                "DobbySourceCommit": SOURCE_SHA,
                "DobbyKeychainAccessGroup": keychain_group,
                "CFBundleExecutable": "DobbyVPN",
            }
        )
    )
    (tunnel / "Info.plist").write_bytes(
        plistlib.dumps(
            {
                "CFBundleIdentifier": ios_archive.TUNNEL_BUNDLE_ID,
                "DobbySourceCommit": SOURCE_SHA,
                "DobbyKeychainAccessGroup": keychain_group,
                "CFBundleExecutable": "Tunnel",
                "NSExtension": {
                    "NSExtensionPointIdentifier": ios_archive.TUNNEL_POINT,
                },
            }
        )
    )
    return app, tunnel


def _source_entitlements() -> tuple[dict[str, object], dict[str, object]]:
    shared = {
        "com.apple.developer.networking.networkextension": [
            "packet-tunnel-provider"
        ],
        "com.apple.security.application-groups": ["group.vpn.dobby.app"],
        "keychain-access-groups": ["$(AppIdentifierPrefix)vpn.dobby.app"],
    }
    app = {
        **shared,
        "com.apple.security.get-task-allow": False,
    }
    return app, dict(shared)


def _write_source_entitlements(
    root: Path,
    app_values: dict[str, object],
    tunnel_values: dict[str, object],
) -> None:
    app_path = root / "app" / "iosApp.entitlements"
    tunnel_path = root / "tunnel" / "tunnel.entitlements"
    app_path.parent.mkdir(parents=True, exist_ok=True)
    tunnel_path.parent.mkdir(parents=True, exist_ok=True)
    app_path.write_bytes(plistlib.dumps(app_values))
    tunnel_path.write_bytes(plistlib.dumps(tunnel_values))
    for bundle in ("app", "tunnel"):
        info_path = root / bundle / "Info.plist"
        info_path.write_bytes(
            plistlib.dumps({"DobbyKeychainAccessGroup": KEYCHAIN_GROUP_TEMPLATE})
        )


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


class IOSArchiveSigningTests(unittest.TestCase):
    def test_signing_expands_source_capabilities_and_signs_tunnel_before_app(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / ios_archive.ARCHIVE_NAME
            app, tunnel = _write_unsigned_archive(archive)
            framework = app / "Frameworks" / "IOSIntegration.framework"
            framework.mkdir(parents=True)
            entitlements_dir = root / "source" / "ios"
            app_source, tunnel_source = _source_entitlements()
            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            output_dir = root / "generated-entitlements"

            with mock.patch.object(ios_archive.subprocess, "run") as run:
                ios_archive.sign_archive_for_export(
                    archive,
                    source_entitlements_dir=entitlements_dir,
                    output_dir=output_dir,
                    team_id=TEAM_ID,
                    identity="Apple Distribution",
                    source_sha=SOURCE_SHA,
                    version=VERSION,
                    build_number=BUILD_NUMBER,
                )

            calls = [call.args[0] for call in run.call_args_list]
            self.assertEqual(len(calls), 3)
            self.assertEqual([command[-1] for command in calls], [str(framework), str(tunnel), str(app)])
            for command in calls:
                self.assertEqual(command[:4], ["codesign", "--force", "--sign", "Apple Distribution"])
            self.assertNotIn("--entitlements", calls[0])
            for command in calls[1:]:
                self.assertIn("--entitlements", command)
                self.assertIn("--generate-entitlement-der", command)
            app_signed = plistlib.loads(
                (output_dir / "app-entitlements.plist").read_bytes()
            )
            tunnel_signed = plistlib.loads(
                (output_dir / "tunnel-entitlements.plist").read_bytes()
            )
            for signed, bundle_id in (
                (app_signed, ios_archive.APP_BUNDLE_ID),
                (tunnel_signed, ios_archive.TUNNEL_BUNDLE_ID),
            ):
                self.assertEqual(signed["application-identifier"], f"{TEAM_ID}.{bundle_id}")
                self.assertEqual(signed["com.apple.developer.team-identifier"], TEAM_ID)
                self.assertEqual(signed["get-task-allow"], False)
                self.assertEqual(
                    signed["com.apple.security.application-groups"],
                    ["group.vpn.dobby.app"],
                )
                self.assertEqual(
                    signed["keychain-access-groups"], [f"{TEAM_ID}.vpn.dobby.app"]
                )
                self.assertEqual(
                    signed["com.apple.developer.networking.networkextension"],
                    ["packet-tunnel-provider"],
                )
                self.assertNotIn("com.apple.security.get-task-allow", signed)
            for bundle in (app, tunnel):
                info = plistlib.loads((bundle / "Info.plist").read_bytes())
                self.assertEqual(
                    info["DobbyKeychainAccessGroup"], f"{TEAM_ID}.vpn.dobby.app"
                )

    def test_signing_rejects_unresolved_substitution_and_entitlement_mismatches(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / ios_archive.ARCHIVE_NAME
            app, _ = _write_unsigned_archive(archive)
            entitlements_dir = root / "source" / "ios"

            def sign(label: str) -> None:
                ios_archive.sign_archive_for_export(
                    archive,
                    source_entitlements_dir=entitlements_dir,
                    output_dir=root / label,
                    team_id=TEAM_ID,
                    identity="Apple Distribution",
                    source_sha=SOURCE_SHA,
                    version=VERSION,
                    build_number=BUILD_NUMBER,
                )

            app_source, tunnel_source = _source_entitlements()
            app_source["keychain-access-groups"] = ["$(UnknownPrefix)vpn.dobby.app"]
            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            with self.assertRaisesRegex(ios_archive.ArchiveError, "unresolved substitution"):
                sign("out-unresolved")

            app_source, tunnel_source = _source_entitlements()
            app_source["application-identifier"] = "OTHER.vpn.dobby.app"
            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            with self.assertRaisesRegex(ios_archive.ArchiveError, "application-identifier"):
                sign("out-application-id")

            app_source, tunnel_source = _source_entitlements()
            tunnel_source["com.apple.developer.team-identifier"] = "OTHERTEAM01"
            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            with self.assertRaisesRegex(ios_archive.ArchiveError, "team-identifier"):
                sign("out-team-id")

            app_source, tunnel_source = _source_entitlements()
            app_source["com.apple.security.get-task-allow"] = True
            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            with self.assertRaisesRegex(ios_archive.ArchiveError, "debugger attachment"):
                sign("out-debugger")

            app_source, tunnel_source = _source_entitlements()
            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            source_info = entitlements_dir / "app" / "Info.plist"
            source_info.write_bytes(
                plistlib.dumps({"DobbyKeychainAccessGroup": "unrelated.group"})
            )
            with self.assertRaisesRegex(ios_archive.ArchiveError, "source Info.plist keychain group"):
                sign("out-source-info-mismatch")

            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            archive_info = plistlib.loads((app / "Info.plist").read_bytes())
            archive_info["DobbyKeychainAccessGroup"] = "unrelated.group"
            (app / "Info.plist").write_bytes(plistlib.dumps(archive_info))
            with self.assertRaisesRegex(ios_archive.ArchiveError, "archive Info.plist keychain group"):
                sign("out-archive-info-mismatch")

    def test_signing_rejects_invalid_team_and_archive_bundle_identifier(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / ios_archive.ARCHIVE_NAME
            _write_unsigned_archive(archive)
            entitlements_dir = root / "source" / "ios"
            app_source, tunnel_source = _source_entitlements()
            _write_source_entitlements(entitlements_dir, app_source, tunnel_source)
            with self.assertRaisesRegex(ios_archive.ArchiveError, "team ID"):
                ios_archive.sign_archive_for_export(
                    archive,
                    source_entitlements_dir=entitlements_dir,
                    output_dir=root / "invalid-team",
                    team_id="lowercase123",
                    identity="Apple Distribution",
                    source_sha=SOURCE_SHA,
                    version=VERSION,
                    build_number=BUILD_NUMBER,
                )

            wrong_archive = root / "wrong" / ios_archive.ARCHIVE_NAME
            _write_unsigned_archive(wrong_archive, app_bundle_id="vpn.wrong.app")
            with self.assertRaisesRegex(ios_archive.ArchiveError, "bundle identifier"):
                ios_archive.sign_archive_for_export(
                    wrong_archive,
                    source_entitlements_dir=entitlements_dir,
                    output_dir=root / "wrong-bundle-id",
                    team_id=TEAM_ID,
                    identity="Apple Distribution",
                    source_sha=SOURCE_SHA,
                    version=VERSION,
                    build_number=BUILD_NUMBER,
                )


class IOSBundleMetadataTests(unittest.TestCase):
    def test_verifier_requires_team_bound_runtime_keychain_group(self) -> None:
        for bundle_id, tunnel in (
            (ios_archive.APP_BUNDLE_ID, False),
            (ios_archive.TUNNEL_BUNDLE_ID, True),
        ):
            info = {
                "CFBundleIdentifier": bundle_id,
                "CFBundleShortVersionString": VERSION,
                "CFBundleVersion": BUILD_NUMBER,
                "DobbySourceCommit": SOURCE_SHA,
                "DobbyKeychainAccessGroup": f"{TEAM_ID}.vpn.dobby.app",
            }
            if tunnel:
                info["NSExtension"] = {
                    "NSExtensionPointIdentifier": "com.apple.networkextension.packet-tunnel",
                    "NSExtensionPrincipalClass": "DobbyVPNTunnel.PacketTunnelProvider",
                }
            arguments = {
                "bundle_id": bundle_id,
                "team_id": TEAM_ID,
                "source_sha": SOURCE_SHA,
                "version": VERSION,
                "build_number": BUILD_NUMBER,
                "tunnel": tunnel,
            }
            verify_ios_app_group.verify_bundle_metadata(info, **arguments)
            for wrong_value in ("vpn.dobby.app", "OTHERTEAM01.vpn.dobby.app"):
                with self.subTest(bundle_id=bundle_id, wrong_value=wrong_value):
                    bad_info = dict(info)
                    bad_info["DobbyKeychainAccessGroup"] = wrong_value
                    with self.assertRaisesRegex(
                        verify_ios_app_group.VerificationError, "keychain access group"
                    ):
                        verify_ios_app_group.verify_bundle_metadata(bad_info, **arguments)


if __name__ == "__main__":
    unittest.main()
