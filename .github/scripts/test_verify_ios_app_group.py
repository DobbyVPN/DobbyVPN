import importlib.util
import datetime
from pathlib import Path
import unittest


SCRIPT = Path(__file__).with_name("verify_ios_app_group.py")
SPEC = importlib.util.spec_from_file_location("verify_ios_app_group", SCRIPT)
assert SPEC and SPEC.loader
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class VerifyIosAppGroupTests(unittest.TestCase):
    def setUp(self) -> None:
        self.team = "TC3Q7MAJXF"

    def values(self, bundle: str, tunnel: bool = False):
        entitlements = {
            "application-identifier": f"{self.team}.{bundle}",
            "com.apple.developer.team-identifier": self.team,
            "com.apple.security.application-groups": [VERIFY.APP_GROUP],
            "get-task-allow": False,
        }
        if tunnel:
            entitlements["com.apple.developer.networking.networkextension"] = ["packet-tunnel-provider"]
        profile_entitlements = dict(entitlements)
        profile = {
            "TeamIdentifier": [self.team],
            "ExpirationDate": datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=30),
            "Entitlements": profile_entitlements,
        }
        entitlements["keychain-access-groups"] = [f"{self.team}.{VERIFY.APP_BUNDLE}"]
        profile_entitlements["keychain-access-groups"] = [f"{self.team}.{VERIFY.APP_BUNDLE}"]
        return entitlements, profile

    def test_app_and_tunnel_exact_contract_passes(self) -> None:
        app, app_profile = self.values(VERIFY.APP_BUNDLE)
        tunnel, tunnel_profile = self.values(VERIFY.TUNNEL_BUNDLE, tunnel=True)
        VERIFY.verify_bundle(app, app_profile, self.team, VERIFY.APP_BUNDLE, tunnel=False)
        VERIFY.verify_bundle(tunnel, tunnel_profile, self.team, VERIFY.TUNNEL_BUNDLE, tunnel=True)

    def test_profile_keychain_wildcard_may_authorize_exact_signed_shared_group(self) -> None:
        actual, profile = self.values(VERIFY.TUNNEL_BUNDLE, tunnel=True)
        profile["Entitlements"]["keychain-access-groups"] = [f"{self.team}.*"]
        VERIFY.verify_bundle(actual, profile, self.team, VERIFY.TUNNEL_BUNDLE, tunnel=True)

    def test_profile_may_authorize_additional_network_extension_kinds(self) -> None:
        tunnel, profile = self.values(VERIFY.TUNNEL_BUNDLE, tunnel=True)
        profile["Entitlements"]["com.apple.developer.networking.networkextension"] = [
            "app-proxy-provider", "packet-tunnel-provider", "dns-proxy",
        ]
        VERIFY.verify_bundle(tunnel, profile, self.team, VERIFY.TUNNEL_BUNDLE, tunnel=True)

    def test_missing_or_wrong_app_group_fails(self) -> None:
        for groups in (None, [], ["group.example.wrong"]):
            actual, profile = self.values(VERIFY.APP_BUNDLE)
            actual["com.apple.security.application-groups"] = groups
            with self.subTest(groups=groups), self.assertRaises(VERIFY.VerificationError):
                VERIFY.verify_bundle(actual, profile, self.team, VERIFY.APP_BUNDLE, tunnel=False)

    def test_wrong_source_bundle_or_missing_packet_tunnel_fails(self) -> None:
        actual, profile = self.values(VERIFY.TUNNEL_BUNDLE, tunnel=True)
        actual["application-identifier"] = f"{self.team}.vpn.dobby.wrong"
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_bundle(actual, profile, self.team, VERIFY.TUNNEL_BUNDLE, tunnel=True)

    def test_bundle_metadata_binds_exact_source_version_build_and_extension(self) -> None:
        source = "a" * 40
        info = {
            "CFBundleIdentifier": VERIFY.TUNNEL_BUNDLE,
            "CFBundleShortVersionString": "1.4.7",
            "CFBundleVersion": "2134",
            "DobbySourceCommit": source,
            "NSExtension": {
                "NSExtensionPointIdentifier": "com.apple.networkextension.packet-tunnel",
                "NSExtensionPrincipalClass": "tunnel.PacketTunnelProvider",
            },
        }
        VERIFY.verify_bundle_metadata(
            info,
            bundle_id=VERIFY.TUNNEL_BUNDLE,
            source_sha=source,
            version="1.4.7",
            build_number="2134",
            tunnel=True,
        )
        for key, replacement in (
            ("DobbySourceCommit", "b" * 40),
            ("CFBundleShortVersionString", "1.4.6"),
            ("CFBundleVersion", "2133"),
            ("CFBundleIdentifier", VERIFY.APP_BUNDLE),
        ):
            changed = dict(info)
            changed[key] = replacement
            with self.subTest(key=key), self.assertRaises(VERIFY.VerificationError):
                VERIFY.verify_bundle_metadata(
                    changed,
                    bundle_id=VERIFY.TUNNEL_BUNDLE,
                    source_sha=source,
                    version="1.4.7",
                    build_number="2134",
                    tunnel=True,
                )

    def test_expired_profile_and_wrong_keychain_group_fail(self) -> None:
        actual, profile = self.values(VERIFY.APP_BUNDLE)
        profile["ExpirationDate"] = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=1)
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_bundle(actual, profile, self.team, VERIFY.APP_BUNDLE, tunnel=False)

        actual, profile = self.values(VERIFY.APP_BUNDLE)
        actual["keychain-access-groups"] = ["WRONG.group"]
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_bundle(actual, profile, self.team, VERIFY.APP_BUNDLE, tunnel=False)
        actual, profile = self.values(VERIFY.APP_BUNDLE)
        actual["get-task-allow"] = True
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_bundle(actual, profile, self.team, VERIFY.APP_BUNDLE, tunnel=False)

    def test_absent_get_task_allow_is_equivalent_to_disabled(self) -> None:
        actual, profile = self.values(VERIFY.APP_BUNDLE)
        actual.pop("get-task-allow")
        VERIFY.verify_bundle(actual, profile, self.team, VERIFY.APP_BUNDLE, tunnel=False)
        actual, profile = self.values(VERIFY.TUNNEL_BUNDLE, tunnel=True)
        actual.pop("com.apple.developer.networking.networkextension")
        with self.assertRaises(VERIFY.VerificationError):
            VERIFY.verify_bundle(actual, profile, self.team, VERIFY.TUNNEL_BUNDLE, tunnel=True)


if __name__ == "__main__":
    unittest.main()
