"""Sign the installed Mac host and both nested extensions with their own profiles."""
import argparse
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile


def sign(app: Path, profiles: Path, identity: str, keychain: Path, development: bool) -> None:
    base = "vpn.dobby.desktop"
    suffix = "" if development else "-systemextension"
    tunnel = "packet-tunnel-provider" + suffix
    filter_capability = "content-filter-provider" + suffix
    profile_type = "MAC_APP_DEVELOPMENT" if development else "MAC_APP_DIRECT"
    ent_key = "com.apple.developer.networking.networkextension"
    flags = ["codesign", "--force", "--sign", identity, "--keychain", str(keychain),
             "--options", "runtime", "--timestamp=none" if development else "--timestamp"]

    with tempfile.TemporaryDirectory() as temp:
        for role, identifier, capabilities in (
            ("provider", base + ".PacketTunnel", [tunnel]),
            ("filter", base + ".Protection", [filter_capability]),
            ("host", base, [tunnel, filter_capability]),
        ):
            bundle = app if role == "host" else app / "Contents/Library/SystemExtensions" / (identifier + ".systemextension")
            contents = bundle / "Contents"
            profile = profiles / f"{role}-{profile_type}.provisionprofile"
            data = plistlib.loads(subprocess.check_output(["security", "cms", "-D", "-i", str(profile)]))
            team = data["TeamIdentifier"][0]
            app_id = f"{team}.{identifier}"
            grants = data["Entitlements"]
            if (app_id != grants.get("com.apple.application-identifier")
                    or not set(capabilities).issubset(grants.get(ent_key, []))):
                raise ValueError(f"{role} profile does not authorize {identifier} and {capabilities}")
            info = plistlib.loads((contents / "Info.plist").read_bytes())
            if info["CFBundleIdentifier"] != identifier:
                raise ValueError(f"unexpected {role} bundle identifier")
            entitlements = {"com.apple.application-identifier": app_id,
                            "com.apple.developer.team-identifier": team, ent_key: capabilities}
            if development:
                entitlements["com.apple.security.get-task-allow"] = True
            if role == "host":
                if not grants.get("com.apple.developer.system-extension.install"):
                    raise ValueError("host profile lacks system extension installation")
                entitlements["com.apple.developer.system-extension.install"] = True
                for executable in ("dobbyvpn-backend", "dobby-cli"):
                    subprocess.run([*flags, contents / "Resources" / executable], check=True)
            else:
                if info["NetworkExtension"]["NEMachServiceName"] != app_id:
                    raise ValueError(f"{role} Mach service and signing team disagree")
                entitlements["com.apple.security.application-groups"] = [app_id]
            options = Path(temp) / f"{role}.entitlements"
            options.write_bytes(plistlib.dumps(entitlements))
            shutil.copyfile(profile, contents / "embedded.provisionprofile")
            subprocess.run([*flags, "--entitlements", options, bundle], check=True)
            subprocess.run(["codesign", "--verify", "--strict", "--verbose=2", bundle], check=True)
    subprocess.run(["codesign", "--verify", "--strict", "--deep", "--verbose=2", app], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("app", type=Path)
    parser.add_argument("--profiles", type=Path, required=True)
    parser.add_argument("--identity", required=True)
    parser.add_argument("--keychain", type=Path, required=True)
    parser.add_argument("--development", action="store_true")
    args = parser.parse_args()
    sign(args.app, args.profiles, args.identity, args.keychain, args.development)
