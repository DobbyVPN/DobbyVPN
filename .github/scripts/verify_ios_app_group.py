#!/usr/bin/env python3
"""Verify an exported iOS app and packet tunnel share the intended App Group."""

from __future__ import annotations

import argparse
import datetime
import plistlib
from pathlib import Path
import re
import sys
from typing import Any


TEAM_ID = re.compile(r"[A-Z0-9]{10}\Z")
APP_GROUP = "group.vpn.dobby.app"
APP_BUNDLE = "vpn.dobby.app"
TUNNEL_BUNDLE = "vpn.dobby.app.tunnel"


class VerificationError(ValueError):
    pass


def load_plist(path: Path, description: str) -> dict[str, Any]:
    if path.is_symlink() or not path.is_file() or path.stat().st_size <= 0:
        raise VerificationError(f"{description} must be a nonempty regular file")
    try:
        value = plistlib.loads(path.read_bytes())
    except (OSError, plistlib.InvalidFileException) as error:
        raise VerificationError(f"{description} is not a valid plist") from error
    if not isinstance(value, dict):
        raise VerificationError(f"{description} must contain a dictionary")
    return value


def require_string_list(value: object, expected: list[str], description: str) -> None:
    if value != expected:
        raise VerificationError(f"{description} does not match the required value")


def require_string_member(value: object, expected: str, description: str) -> None:
    if not isinstance(value, list) or expected not in value or not all(isinstance(item, str) for item in value):
        raise VerificationError(f"{description} does not contain the required value")


def verify_bundle(
    actual: dict[str, Any],
    profile: dict[str, Any],
    team_id: str,
    bundle_id: str,
    *,
    tunnel: bool,
) -> None:
    if not TEAM_ID.fullmatch(team_id):
        raise VerificationError("Apple team ID must be ten uppercase letters or digits")
    profile_entitlements = profile.get("Entitlements")
    if not isinstance(profile_entitlements, dict):
        raise VerificationError("provisioning profile has no entitlement dictionary")
    expected_application = f"{team_id}.{bundle_id}"
    # The app and packet-tunnel extension deliberately share the app's one
    # Keychain access group so the extension can read the opaque secret.
    expected_keychain_group = f"{team_id}.{APP_BUNDLE}"
    for name, entitlements in (("signature", actual), ("profile", profile_entitlements)):
        if entitlements.get("application-identifier") != expected_application:
            raise VerificationError(f"{name} application identifier does not match the selected bundle")
        require_string_list(
            entitlements.get("com.apple.security.application-groups"),
            [APP_GROUP],
            f"{name} App Group",
        )
    require_string_list(
        actual.get("keychain-access-groups"),
        [expected_keychain_group],
        "signature keychain access group",
    )
    profile_keychain = profile_entitlements.get("keychain-access-groups")
    if (
        not isinstance(profile_keychain, list)
        or not all(isinstance(item, str) for item in profile_keychain)
        or not ({expected_keychain_group, f"{team_id}.*"} & set(profile_keychain))
    ):
        raise VerificationError("profile keychain access group does not authorize the shared group")
    if actual.get("com.apple.developer.team-identifier") != team_id:
        raise VerificationError("signature team identifier does not match the selected team")
    teams = profile.get("TeamIdentifier")
    if teams != [team_id]:
        raise VerificationError("provisioning profile team identifier does not match the selected team")
    expiration = profile.get("ExpirationDate")
    if not isinstance(expiration, datetime.datetime):
        raise VerificationError("provisioning profile has no expiration date")
    now = datetime.datetime.now(expiration.tzinfo) if expiration.tzinfo else datetime.datetime.now()
    if expiration <= now:
        raise VerificationError("provisioning profile is expired")
    # Distribution signatures may omit the entitlement entirely; absence and
    # an explicit false both disable debugger attachment. Only true is unsafe.
    if actual.get("get-task-allow", False) is not False:
        raise VerificationError("release signature must disable debugger attachment")
    if tunnel:
        require_string_list(
            actual.get("com.apple.developer.networking.networkextension"),
            ["packet-tunnel-provider"],
            "tunnel signature Network Extension entitlement",
        )
        require_string_member(
            profile_entitlements.get("com.apple.developer.networking.networkextension"),
            "packet-tunnel-provider",
            "tunnel profile Network Extension entitlement",
        )


def verify_bundle_metadata(
    info: dict[str, Any],
    *,
    bundle_id: str,
    source_sha: str,
    version: str,
    build_number: str,
    tunnel: bool,
) -> None:
    if not re.fullmatch(r"[0-9a-f]{40}", source_sha):
        raise VerificationError("source SHA must be a lowercase full commit")
    expected = {
        "CFBundleIdentifier": bundle_id,
        "CFBundleShortVersionString": version,
        "CFBundleVersion": build_number,
        "DobbySourceCommit": source_sha,
    }
    for key, value in expected.items():
        if str(info.get(key, "")) != value:
            raise VerificationError(f"bundle metadata {key} does not match the release input")
    if tunnel:
        extension = info.get("NSExtension")
        if not isinstance(extension, dict):
            raise VerificationError("tunnel bundle has no extension metadata")
        if extension.get("NSExtensionPointIdentifier") != "com.apple.networkextension.packet-tunnel":
            raise VerificationError("tunnel extension point is not packet-tunnel")
        principal = extension.get("NSExtensionPrincipalClass")
        if not isinstance(principal, str) or not principal.endswith(".PacketTunnelProvider"):
            raise VerificationError("tunnel extension principal is invalid")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app-entitlements", required=True, type=Path)
    parser.add_argument("--app-profile", required=True, type=Path)
    parser.add_argument("--tunnel-entitlements", required=True, type=Path)
    parser.add_argument("--tunnel-profile", required=True, type=Path)
    parser.add_argument("--team-id", required=True)
    parser.add_argument("--app-info", required=True, type=Path)
    parser.add_argument("--tunnel-info", required=True, type=Path)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--build-number", required=True)
    args = parser.parse_args()
    try:
        verify_bundle(
            load_plist(args.app_entitlements, "app signature entitlements"),
            load_plist(args.app_profile, "app provisioning profile"),
            args.team_id,
            APP_BUNDLE,
            tunnel=False,
        )
        verify_bundle(
            load_plist(args.tunnel_entitlements, "tunnel signature entitlements"),
            load_plist(args.tunnel_profile, "tunnel provisioning profile"),
            args.team_id,
            TUNNEL_BUNDLE,
            tunnel=True,
        )
        verify_bundle_metadata(
            load_plist(args.app_info, "app Info.plist"),
            bundle_id=APP_BUNDLE,
            source_sha=args.source_sha,
            version=args.version,
            build_number=args.build_number,
            tunnel=False,
        )
        verify_bundle_metadata(
            load_plist(args.tunnel_info, "tunnel Info.plist"),
            bundle_id=TUNNEL_BUNDLE,
            source_sha=args.source_sha,
            version=args.version,
            build_number=args.build_number,
            tunnel=True,
        )
    except VerificationError as error:
        print(f"iOS App Group verification failed: {error}", file=sys.stderr)
        return 1
    print("iOS app, packet tunnel, source, signing, and provisioning metadata verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
