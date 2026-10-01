from __future__ import annotations

from contextlib import redirect_stderr
import io
from pathlib import Path
import subprocess
import unittest
from unittest.mock import call, patch
import xml.etree.ElementTree as ElementTree

from verify_android_apk_source import (
    VerificationError,
    _manifest_output,
    verify_apk,
    verify_apk_manifest,
    verify_code,
    verify_test_companion,
)


SOURCE_SHA = "a" * 40
REPOSITORY = "DobbyVPN/DobbyVPN"
VERSION_NAME = "1.5.2"
VERSION_CODE = 1005002
APK = Path("release.apk")
COMPANION = Path("companion.apk")


def completed(output: str = "", *, returncode: int = 0, stderr: str = "") -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(["apkanalyzer"], returncode, output, stderr)


def build_config(source_sha: str = SOURCE_SHA, repository: str = REPOSITORY) -> str:
    link = f"https://github.com/{repository}/tree/{source_sha}"
    return "\n".join(
        (
            f'.field public static final PROJECT_REPOSITORY_COMMIT:Ljava/lang/String; = "{source_sha}"',
            f'.field public static final PROJECT_REPOSITORY_COMMIT_LINK:Ljava/lang/String; = "{link}"',
        )
    )


def manifest_response(operation: str, *, override: str | None = None) -> subprocess.CompletedProcess[str]:
    values = {
        "application-id": "com.dobby.vpn\n",
        "version-name": f"{VERSION_NAME}\n",
        "version-code": f"{VERSION_CODE}\n",
    }
    return completed(values[operation] if override is None else override)


class AndroidApkSourceVerificationTests(unittest.TestCase):
    def test_application_manifest_and_embedded_source_are_verified_and_output_is_emitted(self) -> None:
        def run(command: list[str]) -> subprocess.CompletedProcess[str]:
            if command[1] == "manifest":
                return manifest_response(command[2])
            if command[1:4] == ["dex", "code", "--class"]:
                return completed(build_config(), stderr="analyzer warning\n")
            self.fail(f"unexpected apkanalyzer command: {command}")

        diagnostics = io.StringIO()
        with (
            patch("verify_android_apk_source.run_apkanalyzer", side_effect=run) as mocked,
            redirect_stderr(diagnostics),
        ):
            verify_apk("apkanalyzer", APK, SOURCE_SHA, REPOSITORY, VERSION_NAME, VERSION_CODE)

        self.assertEqual(
            mocked.call_args_list,
            [
                call(["apkanalyzer", "manifest", "application-id", str(APK)]),
                call(["apkanalyzer", "manifest", "version-name", str(APK)]),
                call(["apkanalyzer", "manifest", "version-code", str(APK)]),
                call(["apkanalyzer", "dex", "code", "--class", "com.dobby.vpn.BuildConfig", str(APK)]),
            ],
        )
        self.assertEqual(
            diagnostics.getvalue(),
            "com.dobby.vpn\n1.5.2\n1005002\n"
            f'{build_config()}\nanalyzer warning\n',
        )

    def test_wrong_or_unresolved_application_metadata_fails_and_is_emitted(self) -> None:
        cases = (
            ("application-id", "com.other.app\n"),
            ("version-name", "1.5.1\n"),
            ("version-code", "1005001\n"),
            ("version-name", "${dobbyVersionName}\n"),
            ("version-code", "${dobbyVersionCode}\n"),
            ("version-name", ""),
        )
        for operation, output in cases:
            with self.subTest(operation=operation, output=output):
                def run(command: list[str]) -> subprocess.CompletedProcess[str]:
                    if command[2] == operation:
                        return manifest_response(operation, override=output)
                    return manifest_response(command[2])

                diagnostics = io.StringIO()
                with (
                    patch("verify_android_apk_source.run_apkanalyzer", side_effect=run),
                    redirect_stderr(diagnostics),
                    self.assertRaisesRegex(VerificationError, "manifest .* mismatch"),
                ):
                    verify_apk_manifest("apkanalyzer", APK, VERSION_NAME, VERSION_CODE)
                if output:
                    self.assertIn(output, diagnostics.getvalue())

    def test_nonzero_manifest_command_preserves_complete_output(self) -> None:
        diagnostics = io.StringIO()
        with (
            patch(
                "verify_android_apk_source.run_apkanalyzer",
                return_value=completed("raw stdout\n", returncode=23, stderr="raw stderr\n"),
            ),
            redirect_stderr(diagnostics),
            self.assertRaisesRegex(VerificationError, "exit code 23"),
        ):
            _manifest_output("apkanalyzer", APK, "version-name", artifact="application APK")

        self.assertIn("raw stdout\n", diagnostics.getvalue())
        self.assertIn("raw stderr\n", diagnostics.getvalue())

    def test_timeout_preserves_output_and_chains_original_exception(self) -> None:
        timeout = subprocess.TimeoutExpired(
            ["apkanalyzer", "manifest", "version-code", str(APK)],
            120,
            output=b"partial stdout\n",
            stderr=b"partial stderr\n",
        )
        diagnostics = io.StringIO()
        with (
            patch("verify_android_apk_source.run_apkanalyzer", side_effect=timeout),
            redirect_stderr(diagnostics),
            self.assertRaisesRegex(VerificationError, "timed out") as raised,
        ):
            _manifest_output("apkanalyzer", APK, "version-code", artifact="application APK")

        self.assertIs(raised.exception.__cause__, timeout)
        self.assertIn("partial stdout\n", diagnostics.getvalue())
        self.assertIn("partial stderr\n", diagnostics.getvalue())

    def test_companion_invalid_xml_preserves_original_parse_error_and_output(self) -> None:
        malformed = "<manifest><meta-data"
        with patch(
            "verify_android_apk_source.run_apkanalyzer",
            side_effect=(completed("com.dobby.vpn.test\n"), completed(malformed)),
        ), redirect_stderr(io.StringIO()) as diagnostics:
            with self.assertRaisesRegex(VerificationError, "invalid XML") as raised:
                verify_test_companion("apkanalyzer", COMPANION, SOURCE_SHA)

        self.assertIsInstance(raised.exception.__cause__, ElementTree.ParseError)
        self.assertIn(malformed, diagnostics.getvalue())

    def test_companion_source_identity_check_remains_active(self) -> None:
        manifest = (
            '<manifest xmlns:android="http://schemas.android.com/apk/res/android">'
            '<application><meta-data android:name="com.dobby.test.source_sha" '
            f'android:value="{SOURCE_SHA}" /></application></manifest>'
        )
        with patch(
            "verify_android_apk_source.run_apkanalyzer",
            side_effect=(completed("com.dobby.vpn.test\n"), completed(manifest)),
        ):
            verify_test_companion("apkanalyzer", COMPANION, SOURCE_SHA)

    def test_embedded_source_commit_and_link_checks_remain_active(self) -> None:
        verify_code(build_config(), SOURCE_SHA, REPOSITORY)
        with self.assertRaisesRegex(VerificationError, "source commit"):
            verify_code(build_config(source_sha="b" * 40), SOURCE_SHA, REPOSITORY)


if __name__ == "__main__":
    unittest.main()
