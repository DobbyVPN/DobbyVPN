#!/usr/bin/env python3
"""Build and inspect the native Debug APK after Android dependency preparation."""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import zipfile

from android_dependency_provenance import ANDROID_BUILD_TOOLS
from normalize_android_apk import normalize_apk
from verify_android_native_payloads import verify

ROOT = Path(__file__).resolve().parents[3]


def run(command: list[str]) -> str:
    print("$ " + " ".join(command), flush=True)
    result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    sys.stdout.buffer.write(result.stdout)
    sys.stderr.buffer.write(result.stderr)
    sys.stdout.flush()
    sys.stderr.flush()
    result.check_returncode()
    return result.stdout.decode("utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--go-binary", default=os.environ.get("GO_BIN") or shutil.which("go"))
    args = parser.parse_args()
    if not args.go_binary:
        parser.error("the pinned Go executable is required")
    sdk = Path(os.environ.get("ANDROID_SDK_ROOT") or os.environ["ANDROID_HOME"])
    gradle = os.environ.get("GRADLE_BIN", str(ROOT / "ui/android/gradlew"))
    run([gradle, "-p", "ui/android", ":app:assembleDebug", "--no-daemon", "--stacktrace",
         f"-PdobbyGoBinary={args.go_binary}", f"-PprojectRepositoryCommit={args.source_sha}"])
    apk = ROOT / "ui/android/app/build/outputs/apk/debug/app-debug.apk"
    analyzer = shutil.which("apkanalyzer") or str(sdk / "cmdline-tools/latest/bin/apkanalyzer")
    if run([analyzer, "manifest", "debuggable", str(apk)]).strip() != "true":
        raise ValueError("Debug APK must allow debugger attachment")
    if run([analyzer, "manifest", "application-id", str(apk)]).strip() != "com.dobby.vpn.debug":
        raise ValueError("Debug APK must use its separate application ID")
    ndk = Path(os.environ["ANDROID_NDK_HOME"])
    readelf, = ndk.glob("toolchains/llvm/prebuilt/*/bin/llvm-readelf")
    verify(apk, readelf)
    with tempfile.TemporaryDirectory(prefix="dobbyvpn-android-debug-") as temporary, zipfile.ZipFile(apk) as archive:
        for abi in ("arm64-v8a", "x86_64"):
            library = Path(temporary) / f"{abi}.so"
            library.write_bytes(archive.read(f"lib/{abi}/libdobby_vpn.so"))
            sections = run([str(readelf), "--sections", str(library)])
            if ".debug_info" not in sections:
                raise ValueError(f"Debug symbols missing for {abi}")
            metadata = run([args.go_binary, "version", "-m", str(library)])
            if "-gcflags=\"all=-N -l\"" not in metadata:
                raise ValueError(f"Go debugging compiler flags missing for {abi}")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(apk, args.output)
    # Match the release compression policy, then restore the development
    # signature that ZIP normalization invalidates.
    build_tools = sdk / "build-tools" / ANDROID_BUILD_TOOLS
    normalize_apk(args.output, build_tools / "zipalign")
    android_user = Path(os.environ.get("ANDROID_USER_HOME", str(Path.home() / ".android")))
    signer = str(build_tools / "apksigner")
    run([signer, "sign", "--ks", str(android_user / "debug.keystore"),
         "--ks-key-alias", "androiddebugkey", "--ks-pass", "pass:android",
         "--key-pass", "pass:android", "--v4-signing-enabled", "false", str(args.output.resolve())])
    run([signer, "verify", "--verbose", str(args.output.resolve())])
    print(f"Verified Debug APK: {args.output}")


if __name__ == "__main__":
    main()
