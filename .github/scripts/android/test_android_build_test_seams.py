#!/usr/bin/env python3
"""Unit checks for the explicit build-local Android test-seams mode."""

from __future__ import annotations

from pathlib import Path
import subprocess
import unittest


PRODUCT_ROOT = Path(__file__).resolve().parents[3]
DRIVER = PRODUCT_ROOT / ".github" / "scripts" / "android" / "android_build_driver.sh"
BUILD_CHECK = PRODUCT_ROOT / ".github" / "scripts" / "android" / "android_build_check.sh"
GRADLE_BUILD = PRODUCT_ROOT / "ui" / "android" / "app" / "build.gradle.kts"


class AndroidBuildTestSeamsTests(unittest.TestCase):
    def run_driver(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["bash", str(DRIVER), "--source-root", str(PRODUCT_ROOT), *arguments],
            cwd=PRODUCT_ROOT,
            text=True,
            capture_output=True,
            check=False,
            timeout=10,
        )

    def test_test_seams_requires_explicit_local_mode(self) -> None:
        result = self.run_driver("--test-seams")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--test-seams requires --local", result.stderr)

    def test_test_seams_rejects_source_identity_and_archived_source(self) -> None:
        identity = "a" * 40
        for options in (
            ("--local", "--test-seams", "--source-sha", identity),
            (
                "--local", "--test-seams", "--trusted-archive-source",
                "--source-sha", identity, "--source-tree", identity,
            ),
            ("--local", "--test-seams", "--manifest", "/tmp/provenance.json"),
        ):
            with self.subTest(options=options):
                result = self.run_driver(*options)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("cannot use source identities, archived/trusted source, or Release provenance outputs", result.stderr)

    def test_build_check_rejects_source_identity_before_toolchain_setup(self) -> None:
        result = subprocess.run(
            [
                "bash", str(BUILD_CHECK), "--source-root", str(PRODUCT_ROOT),
                "--output", "/tmp/test-seams.apk", "--test-companion-output", "/tmp/test-seams-test.apk",
                "--source-sha", "a" * 40, "--test-seams",
            ],
            cwd=PRODUCT_ROOT,
            text=True,
            capture_output=True,
            check=False,
            timeout=10,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot be combined with a source identity or provenance build", result.stderr)

    def test_build_check_forwards_flag_and_keeps_android_abi_validation(self) -> None:
        check = BUILD_CHECK.read_text(encoding="utf-8")
        self.assertIn("--test-seams) test_seams=1", check)
        self.assertIn("driver_args+=(--test-seams)", check)
        self.assertIn("verify_android_native_payloads.py", check)

    def test_gradle_tags_only_explicit_local_test_seams_backend_builds(self) -> None:
        driver = DRIVER.read_text(encoding="utf-8")
        gradle = GRADLE_BUILD.read_text(encoding="utf-8")
        self.assertIn('export DOBBYVPN_BUILD_LOCAL="$local_build"', driver)
        self.assertIn('DOBBYVPN_BUILD_TEST_SEAMS="$test_seams"', driver)
        self.assertIn('providers.environmentVariable("DOBBYVPN_BUILD_TEST_SEAMS")', gradle)
        self.assertIn('!testSeamsBuild || requestedLocalBuild == "1"', gradle)
        self.assertIn("-tags=android,accessibility,static,dobbyvpn_test_seams", gradle)
        self.assertIn('"-trimpath -buildvcs=false"', gradle)


if __name__ == "__main__":
    unittest.main()
