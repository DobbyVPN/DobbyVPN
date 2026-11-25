"""Build and stage the two macOS Network Extension system extensions."""
from __future__ import annotations

import os
from pathlib import Path
import plistlib
import subprocess
import sys

from desktop_build import (
    GO_MODULE_DIR, ROOT_DIR, MACOS_MINIMUM_SYSTEM_VERSION,
    configure_macos_bridge_link, configure_macos_compiler, configure_macos_deployment_target, go_build_identity,
)

# The Mach service names belong to the same signing team as the containing app.
TEAM = "A9RKXATNC8"
EXTENSIONS = {
    "DobbyPacketTunnel": ("PacketTunnel", "packet-tunnel", "DobbyPacketTunnel.PacketTunnel"),
    "DobbyProtection": ("Protection", "filter-packet", "DobbyProtection"),
}


def build(go: str, arch: str, output: Path, debug: bool) -> None:
    env = dict(os.environ, GOOS="darwin", GOARCH=arch, CGO_ENABLED="1")
    configure_macos_compiler(env, arch)
    configure_macos_deployment_target("macos", env)
    configure_macos_bridge_link(env)
    swift_arch = {"arm64": "arm64", "amd64": "x86_64"}[arch]
    triple = f"{swift_arch}-apple-macosx{MACOS_MINIMUM_SYSTEM_VERSION}"
    native = ROOT_DIR / "ui/apple/macos"
    objects = output / ".packet-extension-objects"
    objects.mkdir(parents=True, exist_ok=True)
    archive = objects / "DobbyPacketCore.a"
    lease = objects / "TunnelLease.o"
    def run(*args: str | Path) -> None:
        subprocess.run(args, cwd=GO_MODULE_DIR, env=env, check=True)

    run(go, "build", "-mod=readonly", "-buildmode=c-archive", "-trimpath", "-ldflags", go_build_identity(debug),
        *(["-gcflags=all=-N -l"] if debug else []), "-o", archive, "./cmd/macos-provider")
    module = Path(subprocess.check_output(
        [go, "list", "-m", "-f", "{{.Dir}}", "trusttunnel-go"], cwd=GO_MODULE_DIR, env=env, text=True
    ).strip())
    include = native / "protection"
    header = objects / "ProviderBridge.h"
    header.write_text('#include "DobbyPacketCore.h"\n#include "TunnelLease.h"\n')
    flags = ["-target", triple, "-Wall", "-Werror", *(["-g", "-O0"] if debug else ["-O2"])]
    run("xcrun", "clang", *flags, "-c", include / "TunnelLease.c", "-o", lease)
    frameworks = [part for name in ("Foundation", "CoreFoundation", "Security", "Network",
                  "NetworkExtension", "SystemConfiguration") for part in ("-framework", name)]
    run("xcrun", "swiftc", "-target", triple, "-module-name", "DobbyPacketTunnel",
        *(["-g", "-Onone"] if debug else ["-O"]), "-I", include, "-import-objc-header", header,
        native / "provider/main.swift", lease, archive, module / "lib/macos/libdobby_bridge.a",
        "-lc++", "-lresolv", *frameworks, "-o", output / "DobbyPacketTunnel")
    run("xcrun", "clang", *flags, "-fobjc-arc", include / "Protection.m", lease, *frameworks,
        "-o", output / "DobbyProtection")


def stage(contents: Path, source: Path, version: str) -> None:
    import shutil
    for executable, (suffix, kind, cls) in EXTENSIONS.items():
        identifier = f"vpn.dobby.desktop.{suffix}"
        dest = contents / "Library/SystemExtensions" / f"{identifier}.systemextension" / "Contents"
        (dest / "MacOS").mkdir(parents=True)
        shutil.copy2(source / executable, dest / "MacOS" / executable)
        info = {
            "CFBundleIdentifier": identifier, "CFBundleExecutable": executable,
            "CFBundleName": f"DobbyVPN {suffix}", "CFBundlePackageType": "SYSX",
            "CFBundleVersion": version, "CFBundleShortVersionString": version,
            "LSMinimumSystemVersion": MACOS_MINIMUM_SYSTEM_VERSION,
            "NSSystemExtensionUsageDescription": "DobbyVPN manages VPN traffic and protects connections during recovery.",
            "NetworkExtension": {"NEMachServiceName": f"{TEAM}.{identifier}",
                "NEProviderClasses": {f"com.apple.networkextension.{kind}": cls}},
        }
        (dest / "Info.plist").write_bytes(plistlib.dumps(info))


if __name__ == "__main__":
    build(sys.argv[1], sys.argv[2], Path(sys.argv[3]).resolve(), "--debug" in sys.argv[4:])
