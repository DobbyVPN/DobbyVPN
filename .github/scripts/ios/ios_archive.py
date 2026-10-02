#!/usr/bin/env python3
"""Package and validate the unsigned iOS Xcode archive used by Release."""

from __future__ import annotations

import argparse
import copy
import os
from pathlib import Path, PurePosixPath
import plistlib
import re
import shutil
import subprocess
import struct
import sys
import tarfile
import zipfile

SOURCE_SHA = re.compile(r"^[0-9a-f]{40}$")
VERSION = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
ARCHIVE_NAME = "DobbyVPN.xcarchive"
APP_BUNDLE_ID = "vpn.dobby.app"
TUNNEL_BUNDLE_ID = "vpn.dobby.app.tunnel"
TUNNEL_POINT = "com.apple.networkextension.packet-tunnel"
TEAM_ID = re.compile(r"^[A-Z0-9]{10}$")
UNRESOLVED_SUBSTITUTION = re.compile(r"\$\([^)]*\)|\$\{[^}]*\}")
APP_GET_TASK_ALLOW = "com.apple.security.get-task-allow"
GET_TASK_ALLOW = "get-task-allow"
KEYCHAIN_GROUPS = "keychain-access-groups"


class ArchiveError(ValueError):
    """An iOS archive is missing expected product content or metadata."""


def _read_plist(path: Path, label: str) -> dict[str, object]:
    try:
        value = plistlib.loads(path.read_bytes())
    except (OSError, plistlib.InvalidFileException) as error:
        raise ArchiveError(f"{label} is not a readable property list") from error
    if not isinstance(value, dict):
        raise ArchiveError(f"{label} must be a property-list dictionary")
    return value


def _bundle_info(
    app: Path,
    *,
    source_sha: str,
    version: str,
    build_number: str,
) -> tuple[Path, Path]:
    app_info = _read_plist(app / "Info.plist", "iOS app Info.plist")
    if app_info.get("CFBundleIdentifier") != APP_BUNDLE_ID:
        raise ArchiveError("iOS app bundle identifier is incorrect")
    if app_info.get("CFBundleShortVersionString") != version:
        raise ArchiveError("iOS app marketing version does not match Release metadata")
    if str(app_info.get("CFBundleVersion", "")) != build_number:
        raise ArchiveError("iOS app build number does not match Release metadata")
    if app_info.get("DobbySourceCommit") != source_sha:
        raise ArchiveError("iOS app source commit does not match selected source")
    executable_name = app_info.get("CFBundleExecutable")
    if not isinstance(executable_name, str) or not executable_name:
        raise ArchiveError("iOS app executable name is missing")
    executable = app / executable_name
    if not executable.is_file() or not os.access(executable, os.X_OK):
        raise ArchiveError("iOS app executable is missing or not executable")

    tunnels = sorted((app / "PlugIns").glob("*.appex"))
    if len(tunnels) != 1:
        raise ArchiveError("iOS app must contain exactly one NetworkExtension tunnel")
    tunnel = tunnels[0]
    tunnel_info = _read_plist(tunnel / "Info.plist", "iOS tunnel Info.plist")
    if tunnel_info.get("CFBundleIdentifier") != TUNNEL_BUNDLE_ID:
        raise ArchiveError("iOS tunnel bundle identifier is incorrect")
    if tunnel_info.get("DobbySourceCommit") != source_sha:
        raise ArchiveError("iOS tunnel source commit does not match selected source")
    extension = tunnel_info.get("NSExtension")
    if not isinstance(extension, dict) or extension.get("NSExtensionPointIdentifier") != TUNNEL_POINT:
        raise ArchiveError("iOS tunnel is not a packet-tunnel NetworkExtension")
    tunnel_executable_name = tunnel_info.get("CFBundleExecutable")
    if not isinstance(tunnel_executable_name, str) or not tunnel_executable_name:
        raise ArchiveError("iOS tunnel executable name is missing")
    tunnel_executable = tunnel / tunnel_executable_name
    if not tunnel_executable.is_file() or not os.access(tunnel_executable, os.X_OK):
        raise ArchiveError("iOS tunnel executable is missing or not executable")
    return app, tunnel


def _validate_archive(
    archive: Path,
    *,
    source_sha: str,
    version: str,
    build_number: str,
) -> tuple[Path, Path]:
    if not SOURCE_SHA.fullmatch(source_sha):
        raise ArchiveError("source SHA must be a lowercase 40-character Git SHA")
    if not VERSION.fullmatch(version):
        raise ArchiveError("iOS version must use X.Y.Z format")
    if not build_number.isdigit() or int(build_number) < 1:
        raise ArchiveError("iOS build number must be a positive integer")
    if not archive.is_dir() or archive.name != ARCHIVE_NAME:
        raise ArchiveError(f"Xcode archive must be a directory named {ARCHIVE_NAME}")
    products = archive / "Products" / "Applications"
    apps = sorted(products.glob("*.app"))
    if len(apps) != 1:
        raise ArchiveError("Xcode archive must contain exactly one iOS app")
    return _bundle_info(
        apps[0],
        source_sha=source_sha,
        version=version,
        build_number=build_number,
    )


def _expand_entitlement_value(value: object, team_id: str) -> object:
    """Expand the supported source entitlement variable and reject leftovers."""
    if isinstance(value, dict):
        return {
            key: _expand_entitlement_value(item, team_id)
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [_expand_entitlement_value(item, team_id) for item in value]
    if isinstance(value, str):
        expanded = value.replace("$(AppIdentifierPrefix)", f"{team_id}.")
        if UNRESOLVED_SUBSTITUTION.search(expanded):
            raise ArchiveError("source iOS entitlements contain an unresolved substitution")
        return expanded
    return value


def _export_entitlements(
    source: Path,
    *,
    team_id: str,
    bundle_id: str,
) -> dict[str, object]:
    values = _expand_entitlement_value(
        _read_plist(source, f"source iOS entitlements ({source.name})"), team_id
    )
    if not isinstance(values, dict):
        raise ArchiveError("source iOS entitlements must contain a dictionary")

    source_get_task_allow = values.pop(APP_GET_TASK_ALLOW, False)
    if source_get_task_allow is not False or values.get(GET_TASK_ALLOW, False) is not False:
        raise ArchiveError("source iOS entitlements must disable debugger attachment")
    values[GET_TASK_ALLOW] = False

    expected_application = f"{team_id}.{bundle_id}"
    expected_keychain_group = f"{team_id}.{APP_BUNDLE_ID}"
    if values.get(KEYCHAIN_GROUPS) != [expected_keychain_group]:
        raise ArchiveError("source keychain group does not match the shared app group")
    for key, expected in (
        ("application-identifier", expected_application),
        ("com.apple.developer.team-identifier", team_id),
    ):
        if key in values and values[key] != expected:
            raise ArchiveError(f"source iOS entitlement {key} conflicts with the selected signing identity")
        values[key] = expected
    return values


def _bind_info_keychain_group(
    archive_info_path: Path,
    source_info_path: Path,
    *,
    signing_entitlements: dict[str, object],
    team_id: str,
) -> None:
    source_info = _read_plist(source_info_path, f"source Info.plist ({source_info_path.name})")
    source_group = _expand_entitlement_value(
        source_info.get("DobbyKeychainAccessGroup"), team_id
    )
    if not isinstance(source_group, str) or signing_entitlements.get(KEYCHAIN_GROUPS) != [source_group]:
        raise ArchiveError("source Info.plist keychain group does not match its signing entitlement")

    archive_info = _read_plist(archive_info_path, f"archive Info.plist ({archive_info_path.name})")
    if archive_info.get("DobbyKeychainAccessGroup") not in (APP_BUNDLE_ID, source_group):
        raise ArchiveError("archive Info.plist keychain group is unrelated to the selected app")
    archive_info["DobbyKeychainAccessGroup"] = source_group
    archive_info_path.write_bytes(plistlib.dumps(archive_info))


def sign_archive_for_export(
    archive: Path,
    *,
    source_entitlements_dir: Path,
    output_dir: Path,
    team_id: str,
    identity: str,
    source_sha: str,
    version: str,
    build_number: str,
) -> None:
    """Apply selected source entitlements and sign the extracted archive in place."""
    if not TEAM_ID.fullmatch(team_id):
        raise ArchiveError("Apple team ID must be ten uppercase letters or digits")
    if not identity:
        raise ArchiveError("iOS signing identity is empty")
    app, tunnel = _validate_archive(
        archive,
        source_sha=source_sha,
        version=version,
        build_number=build_number,
    )
    app_entitlements = _export_entitlements(
        source_entitlements_dir / "app" / "iosApp.entitlements",
        team_id=team_id,
        bundle_id=APP_BUNDLE_ID,
    )
    tunnel_entitlements = _export_entitlements(
        source_entitlements_dir / "tunnel" / "tunnel.entitlements",
        team_id=team_id,
        bundle_id=TUNNEL_BUNDLE_ID,
    )
    _bind_info_keychain_group(
        app / "Info.plist",
        source_entitlements_dir / "app" / "Info.plist",
        signing_entitlements=app_entitlements,
        team_id=team_id,
    )
    _bind_info_keychain_group(
        tunnel / "Info.plist",
        source_entitlements_dir / "tunnel" / "Info.plist",
        signing_entitlements=tunnel_entitlements,
        team_id=team_id,
    )
    output_dir.mkdir(parents=True, exist_ok=True)
    app_entitlements_path = output_dir / "app-entitlements.plist"
    tunnel_entitlements_path = output_dir / "tunnel-entitlements.plist"
    app_entitlements_path.write_bytes(plistlib.dumps(app_entitlements))
    tunnel_entitlements_path.write_bytes(plistlib.dumps(tunnel_entitlements))

    # Seal embedded frameworks before the extension and containing app.
    frameworks = sorted(app.rglob("*.framework"), key=lambda path: len(path.parts), reverse=True)
    signing = [(framework, None) for framework in frameworks]
    signing.extend(((tunnel, tunnel_entitlements_path), (app, app_entitlements_path)))
    for bundle, entitlements in signing:
        command = ["codesign", "--force", "--sign", identity]
        if entitlements is not None:
            command.extend(
                ["--entitlements", str(entitlements), "--generate-entitlement-der"]
            )
        command.append(str(bundle))
        subprocess.run(command, check=True)


def _safe_tar_members(archive: tarfile.TarFile) -> list[tarfile.TarInfo]:
    members = archive.getmembers()
    if not members:
        raise ArchiveError("iOS archive tarball is empty")
    roots: set[str] = set()
    for member in members:
        path = PurePosixPath(member.name)
        if path.is_absolute() or ".." in path.parts or "\\" in member.name:
            raise ArchiveError("iOS archive tarball contains an unsafe path")
        if not path.parts:
            raise ArchiveError("iOS archive tarball contains an empty path")
        roots.add(path.parts[0])
        if member.isdev() or member.isfifo():
            raise ArchiveError("iOS archive tarball contains a special file")
        if member.issym():
            link = PurePosixPath(member.linkname)
            if link.is_absolute() or ".." in link.parts or "\\" in member.linkname:
                raise ArchiveError("iOS archive tarball contains an unsafe symbolic link")
        if member.islnk():
            link = PurePosixPath(member.linkname)
            if link.is_absolute() or ".." in link.parts or "\\" in member.linkname:
                raise ArchiveError("iOS archive tarball contains an unsafe hard link")
    if roots != {ARCHIVE_NAME}:
        raise ArchiveError("iOS archive tarball must contain only DobbyVPN.xcarchive")
    return members


def pack_archive(
    archive: Path,
    output: Path,
    *,
    source_sha: str,
    version: str,
    build_number: str,
) -> None:
    _validate_archive(
        archive,
        source_sha=source_sha,
        version=version,
        build_number=build_number,
    )
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_name(f".{output.name}.tmp")
    try:
        with tarfile.open(temporary, mode="w:gz", format=tarfile.PAX_FORMAT) as tar:
            tar.add(archive, arcname=ARCHIVE_NAME, recursive=True)
        with tarfile.open(temporary, mode="r:gz") as tar:
            _safe_tar_members(tar)
        temporary.replace(output)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise


def unpack_archive(input_tar: Path, output_dir: Path) -> Path:
    if not input_tar.is_file():
        raise ArchiveError("iOS archive tarball is missing")
    output_dir.mkdir(parents=True, exist_ok=True)
    with tarfile.open(input_tar, mode="r:gz") as tar:
        members = _safe_tar_members(tar)
        # The archive is produced by our own build command and its entries are
        # constrained to one root with no upward links. The data filter adds
        # a second path check on supported Python versions.
        if sys.version_info >= (3, 12):
            tar.extractall(output_dir, members=members, filter="data")
        else:
            tar.extractall(output_dir, members=members)
    result = output_dir / ARCHIVE_NAME
    if not result.is_dir():
        raise ArchiveError("iOS archive tarball did not extract the expected archive")
    return result


def verify_ipa(
    ipa: Path,
    *,
    source_sha: str,
    version: str,
    build_number: str,
) -> None:
    if not ipa.is_file():
        raise ArchiveError("signed iOS IPA is missing")
    try:
        with zipfile.ZipFile(ipa) as bundle:
            apps = [
                name.rstrip("/")
                for name in bundle.namelist()
                if re.fullmatch(r"Payload/[^/]+\.app/Info\.plist", name)
            ]
            if len(apps) != 1:
                raise ArchiveError("signed iOS IPA must contain exactly one app")
            app_info_name = apps[0]
            app_root = app_info_name.removesuffix("/Info.plist")
            app_info = plistlib.loads(bundle.read(app_info_name))
            if not isinstance(app_info, dict):
                raise ArchiveError("signed iOS app Info.plist is not a dictionary")
            if app_info.get("CFBundleIdentifier") != APP_BUNDLE_ID:
                raise ArchiveError("signed iOS app bundle identifier is incorrect")
            if app_info.get("CFBundleShortVersionString") != version:
                raise ArchiveError("signed iOS app marketing version does not match Release metadata")
            if str(app_info.get("CFBundleVersion", "")) != build_number:
                raise ArchiveError("signed iOS app build number does not match Release metadata")
            if app_info.get("DobbySourceCommit") != source_sha:
                raise ArchiveError("signed iOS app source commit does not match selected source")
            tunnel_info_names = [
                name
                for name in bundle.namelist()
                if name.startswith(f"{app_root}/PlugIns/")
                and name.endswith(".appex/Info.plist")
            ]
            if len(tunnel_info_names) != 1:
                raise ArchiveError("signed iOS app must contain exactly one tunnel extension")
            tunnel_info = plistlib.loads(bundle.read(tunnel_info_names[0]))
            if not isinstance(tunnel_info, dict):
                raise ArchiveError("signed iOS tunnel Info.plist is not a dictionary")
            extension = tunnel_info.get("NSExtension")
            if (
                tunnel_info.get("CFBundleIdentifier") != TUNNEL_BUNDLE_ID
                or tunnel_info.get("DobbySourceCommit") != source_sha
                or not isinstance(extension, dict)
                or extension.get("NSExtensionPointIdentifier") != TUNNEL_POINT
            ):
                raise ArchiveError("signed iOS tunnel content does not match the qualified archive")
    except (OSError, zipfile.BadZipFile, plistlib.InvalidFileException) as error:
        raise ArchiveError("signed iOS IPA is not a valid app archive") from error


def compress_ipa(ipa: Path) -> None:
    """Repack an IPA with maximum ZIP Deflate compression, preserving entries."""
    if not ipa.is_file():
        raise ArchiveError("signed iOS IPA is missing")
    temporary = ipa.with_name(f".{ipa.name}.compressed.tmp")
    try:
        with zipfile.ZipFile(ipa, mode="r") as source:
            with zipfile.ZipFile(
                temporary,
                mode="w",
                compression=zipfile.ZIP_DEFLATED,
                compresslevel=9,
            ) as compressed:
                compressed.comment = source.comment
                for source_info in source.infolist():
                    compressed_info = copy.copy(source_info)
                    compressed_info.compress_type = zipfile.ZIP_DEFLATED
                    compressed_info._compresslevel = 9
                    compressed_info.extra = _ipa_extra_without_zip64(source_info.extra)
                    with source.open(source_info, mode="r") as source_file:
                        with compressed.open(compressed_info, mode="w") as compressed_file:
                            shutil.copyfileobj(source_file, compressed_file, length=1024 * 1024)
                    # zipfile supplies default permissions for a zero value.
                    compressed_info.external_attr = source_info.external_attr
        with zipfile.ZipFile(temporary, mode="r") as compressed:
            if any(info.compress_type != zipfile.ZIP_DEFLATED for info in compressed.infolist()):
                raise ArchiveError("signed iOS IPA was not fully compressed")
        temporary.replace(ipa)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise


def _ipa_extra_without_zip64(extra: bytes) -> bytes:
    """Drop stale ZIP64 size and offset metadata so zipfile can regenerate it."""
    retained = bytearray()
    offset = 0
    while offset < len(extra):
        if len(extra) - offset < 4:
            raise ArchiveError("signed iOS IPA contains a truncated ZIP extra field")
        field_id, field_size = struct.unpack_from("<HH", extra, offset)
        end = offset + 4 + field_size
        if end > len(extra):
            raise ArchiveError("signed iOS IPA contains a truncated ZIP extra field")
        if field_id != 0x0001:
            retained.extend(extra[offset:end])
        offset = end
    return bytes(retained)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("verify", "pack"):
        command = commands.add_parser(name)
        command.add_argument("--archive-dir", type=Path, required=True)
        command.add_argument("--source-sha", required=True)
        command.add_argument("--version", required=True)
        command.add_argument("--build-number", required=True)
        if name == "pack":
            command.add_argument("--output", type=Path, required=True)
    extract = commands.add_parser("extract")
    extract.add_argument("--input", type=Path, required=True)
    extract.add_argument("--output-dir", type=Path, required=True)
    ipa = commands.add_parser("verify-ipa")
    ipa.add_argument("--ipa", type=Path, required=True)
    ipa.add_argument("--source-sha", required=True)
    ipa.add_argument("--version", required=True)
    ipa.add_argument("--build-number", required=True)
    compress = commands.add_parser("compress-ipa")
    compress.add_argument("--ipa", type=Path, required=True)
    sign = commands.add_parser("sign-for-export")
    sign.add_argument("--archive-dir", type=Path, required=True)
    sign.add_argument("--source-entitlements-dir", type=Path, required=True)
    sign.add_argument("--output-dir", type=Path, required=True)
    sign.add_argument("--team-id", required=True)
    sign.add_argument("--identity", required=True)
    sign.add_argument("--source-sha", required=True)
    sign.add_argument("--version", required=True)
    sign.add_argument("--build-number", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "extract":
            archive = unpack_archive(args.input, args.output_dir)
            print(f"Extracted unsigned iOS archive: {archive}")
        elif args.command == "verify-ipa":
            verify_ipa(
                args.ipa,
                source_sha=args.source_sha,
                version=args.version,
                build_number=args.build_number,
            )
            print("Signed iOS IPA metadata matches the qualified source and version")
        elif args.command == "compress-ipa":
            compress_ipa(args.ipa)
            print(f"Compressed signed iOS IPA: {args.ipa}")
        elif args.command == "sign-for-export":
            sign_archive_for_export(
                args.archive_dir,
                source_entitlements_dir=args.source_entitlements_dir,
                output_dir=args.output_dir,
                team_id=args.team_id,
                identity=args.identity,
                source_sha=args.source_sha,
                version=args.version,
                build_number=args.build_number,
            )
            print("Signed the extracted iOS archive with selected source entitlements")
        else:
            _validate_archive(
                args.archive_dir,
                source_sha=args.source_sha,
                version=args.version,
                build_number=args.build_number,
            )
            if args.command == "pack":
                pack_archive(
                    args.archive_dir,
                    args.output,
                    source_sha=args.source_sha,
                    version=args.version,
                    build_number=args.build_number,
                )
                print(f"Verified and packed unsigned iOS archive: {args.output}")
            else:
                print("Unsigned iOS archive content matches Release metadata")
        return 0
    except (
        OSError,
        subprocess.CalledProcessError,
        tarfile.TarError,
        ArchiveError,
        plistlib.InvalidFileException,
    ) as error:
        print(f"iOS archive validation failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
