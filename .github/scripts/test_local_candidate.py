from __future__ import annotations

import io
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("local_candidate.py")
SPEC = importlib.util.spec_from_file_location("local_candidate", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
candidate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(candidate)


class LocalCandidateTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.request = Path(self.temporary.name)
        self.source = self.request / "source"
        self.source.mkdir()

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _desktop_outputs(self, platform: str) -> None:
        go = self.source / "go_module"
        go.mkdir(exist_ok=True)
        (go / candidate.SERVICE_NAMES[platform]).write_bytes(b"service")
        (go / candidate.CLI_NAMES[platform]).write_bytes(b"cli")
        (go / candidate.UI_TEST_NAMES[platform]).write_bytes(b"ui-test")
        if platform in {"windows", "macos"}:
            (go / candidate.UI_NAMES[platform]).write_bytes(b"ui")

    def test_prepare_describes_each_desktop_candidate_with_confined_paths(self) -> None:
        for platform in candidate.DESKTOP_PLATFORMS:
            with self.subTest(platform=platform):
                self.source = self.request / f"source-{platform}"
                self.source.mkdir()
                self._desktop_outputs(platform)
                output = self.request / f"{platform}.json"
                with mock.patch.object(candidate, "_build_desktop"):
                    descriptor = candidate.prepare_candidate(
                        request_root=self.request,
                        source_root=self.source,
                        platform=platform,
                        output=output,
                    )
                expected = {"cli", "service", "network"}
                if platform in {"windows", "macos"}:
                    expected.add("ui_test")
                    expected.add("ui")
                self.assertEqual(set(descriptor), expected)
                self.assertEqual(json.loads(output.read_text()), descriptor)
                for interface in expected:
                    self.assertTrue(Path(descriptor[interface]).is_relative_to(self.request))
                self.assertEqual(
                    descriptor["network"],
                    str((self.source.parent if platform == "linux" else self.source) / ".dobbyvpn-run" / "s"),
                )

    def test_macos_default_matches_host_and_explicit_architecture_wins(self) -> None:
        self._desktop_outputs("macos")
        for machine, requested, expected in (
            ("x86_64", None, "amd64"),
            ("arm64", None, "arm64"),
            ("x86_64", "arm64", "arm64"),
        ):
            with self.subTest(machine=machine, requested=requested), mock.patch.object(
                candidate.host_platform, "machine", return_value=machine
            ), mock.patch.object(candidate, "_build_desktop") as build:
                descriptor = candidate.prepare_candidate(
                    request_root=self.request, source_root=self.source,
                    platform="macos", architecture=requested,
                    output=self.request / "macos.json",
                )
                self.assertEqual(build.call_args.args[2], expected)

    def test_prepare_describes_android_paths(self) -> None:
        apk = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-release-unsigned.apk"
        companion = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-test-companion.apk"

        def fake_android(source: Path, root: Path, architecture: str) -> Path:
            self.assertEqual(source, self.source)
            self.assertEqual(architecture, "arm64-v8a")
            apk.parent.mkdir(parents=True, exist_ok=True)
            apk.write_bytes(b"apk")
            companion.write_bytes(b"companion")
            return apk

        with mock.patch.object(candidate, "_build_android", side_effect=fake_android):
            descriptor = candidate.prepare_candidate(
                request_root=self.request,
                source_root=self.source,
                platform="android",
                output=self.request / "android.json",
            )
        self.assertEqual(set(descriptor), {"app", "test_companion"})
        self.assertEqual(descriptor["app"], str(apk))
        self.assertEqual(descriptor["test_companion"], str(companion))

    def test_prepare_uses_precreated_request_logs(self) -> None:
        self._desktop_outputs("linux")
        logs = self.request / "logs"
        logs.mkdir(mode=0o700)
        app_log = logs / "app.log"
        service_log = logs / "service.log"
        app_log.write_bytes(b"existing app log\n")
        service_log.write_bytes(b"existing service log\n")
        app_log.chmod(0o600)
        service_log.chmod(0o600)

        with mock.patch.object(candidate, "_build_desktop"):
            descriptor = candidate.prepare_candidate(
                request_root=self.request,
                source_root=self.source,
                platform="linux",
                output=self.request / "linux.json",
            )

        self.assertNotIn("logs", descriptor)
        self.assertFalse((self.source / ".dobbyvpn-local-candidate" / "logs").exists())
        self.assertEqual(app_log.read_bytes(), b"existing app log\n")
        self.assertEqual(service_log.read_bytes(), b"existing service log\n")

    def test_android_apksigner_is_the_driver_pinned_version(self) -> None:
        sdk = self.request / "android-sdk"
        pinned = sdk / "build-tools" / candidate.ANDROID_BUILD_TOOLS_VERSION / "apksigner"
        pinned.parent.mkdir(parents=True)
        pinned.write_bytes(b"pinned")
        pinned.chmod(0o700)
        alternate = self.request / "bin" / "apksigner"
        alternate.parent.mkdir()
        alternate.write_bytes(b"alternate")
        alternate.chmod(0o700)

        with mock.patch.dict(
            candidate.os.environ,
            {"ANDROID_SDK_ROOT": str(sdk), "PATH": str(alternate.parent)},
            clear=True,
        ):
            self.assertEqual(candidate._android_apksigner(), pinned.resolve())

        pinned.unlink()
        with mock.patch.dict(
            candidate.os.environ,
            {"ANDROID_SDK_ROOT": str(sdk), "PATH": str(alternate.parent)},
            clear=True,
        ):
            with self.assertRaisesRegex(
                candidate.CandidateError,
                "pinned Android apksigner is unavailable",
            ):
                candidate._android_apksigner()

    def test_android_build_requests_the_companion_and_signs_both_outputs(self) -> None:
        helper = self.source / ".github" / "scripts" / "android_build_driver.sh"
        helper.parent.mkdir(parents=True)
        helper.write_text("#!/bin/sh\n")
        helper.chmod(0o700)
        candidate_root = self.source / ".dobbyvpn-local-candidate"
        unsigned = candidate_root / "dobbyvpn-release-unsigned.apk"
        unsigned_companion = candidate_root / "dobbyvpn-test-companion-unsigned.apk"
        signed = candidate_root / "dobbyvpn-release.apk"
        signed_companion = candidate_root / "dobbyvpn-test-companion.apk"

        commands: list[list[str]] = []

        def fake_run(command: list[str], *, source_root: Path) -> None:
            self.assertEqual(source_root, self.source)
            commands.append(command)
            candidate_root.mkdir(parents=True, exist_ok=True)
            unsigned.write_bytes(b"unsigned")
            unsigned_companion.write_bytes(b"unsigned companion")

        with mock.patch.object(candidate, "_run", side_effect=fake_run), mock.patch.object(
            candidate, "_sign_android_pair"
        ) as sign:
            result = candidate._build_android(self.source, candidate_root, "arm64-v8a")

        self.assertEqual(result, signed)
        self.assertEqual(len(commands), 1)
        self.assertIn("--local", commands[0])
        self.assertIn("--test-companion-output", commands[0])
        self.assertEqual(
            commands[0][commands[0].index("--test-companion-output") + 1],
            str(unsigned_companion),
        )
        sign.assert_called_once_with(unsigned, unsigned_companion, signed, signed_companion)
        self.assertFalse(unsigned.exists())
        self.assertFalse(unsigned_companion.exists())

    def test_android_build_rejects_missing_application_apk(self) -> None:
        helper = self.source / ".github" / "scripts" / "android_build_driver.sh"
        helper.parent.mkdir(parents=True)
        helper.write_text("#!/bin/sh\n")
        helper.chmod(0o700)
        candidate_root = self.source / ".dobbyvpn-local-candidate"
        unsigned_companion = candidate_root / "dobbyvpn-test-companion-unsigned.apk"

        def fake_run(_command: list[str], *, source_root: Path) -> None:
            self.assertEqual(source_root, self.source)
            candidate_root.mkdir(parents=True, exist_ok=True)
            unsigned_companion.write_bytes(b"unsigned companion")

        with mock.patch.object(candidate, "_run", side_effect=fake_run), mock.patch.object(
            candidate, "_sign_android_pair"
        ) as sign:
            with self.assertRaisesRegex(
                candidate.CandidateError,
                "Android build did not produce the application APK",
            ):
                candidate._build_android(self.source, candidate_root, "arm64-v8a")

        sign.assert_not_called()

    def test_android_signer_digest_accepts_one_exact_sha256(self) -> None:
        digest = "ab" * 32
        completed = candidate.subprocess.CompletedProcess(
            ["apksigner"],
            0,
            stdout=(f"Signer #1 certificate SHA-256 digest: {digest}\n").encode(),
            stderr=b"",
        )
        with mock.patch.object(candidate.subprocess, "run", return_value=completed):
            self.assertEqual(
                candidate._android_signer_digest(
                    Path("/tools/apksigner"),
                    Path("/candidate/app.apk"),
                    {},
                ),
                digest,
            )

        duplicate = candidate.subprocess.CompletedProcess(
            ["apksigner"],
            0,
            stdout=(
                f"Signer #1 certificate SHA-256 digest: {digest}\n"
                f"Signer #2 certificate SHA-256 digest: {'cd' * 32}\n"
            ).encode(),
            stderr=b"",
        )
        with mock.patch.object(candidate.subprocess, "run", return_value=duplicate):
            with self.assertRaisesRegex(
                candidate.CandidateError,
                "exactly one signer certificate",
            ):
                candidate._android_signer_digest(
                    Path("/tools/apksigner"),
                    Path("/candidate/app.apk"),
                    {},
                )

    def test_android_signing_retains_nested_tool_stdout_and_stderr(self) -> None:
        candidate_root = self.request / "candidate"
        candidate_root.mkdir()
        unsigned_app = candidate_root / "unsigned.apk"
        unsigned_companion = candidate_root / "unsigned-companion.apk"
        signed_app = candidate_root / "signed.apk"
        signed_companion = candidate_root / "signed-companion.apk"
        unsigned_app.write_bytes(b"app")
        unsigned_companion.write_bytes(b"companion")
        digest = "ab" * 32
        completed = [
            candidate.subprocess.CompletedProcess(
                ["keytool"], 0, stdout=b"", stderr=b"keytool stderr\n"
            ),
            candidate.subprocess.CompletedProcess(
                ["apksigner", "sign", "app"],
                0,
                stdout=b"app sign stdout\n",
                stderr=b"app sign stderr\n",
            ),
            candidate.subprocess.CompletedProcess(
                ["apksigner", "sign", "companion"],
                0,
                stdout=b"companion sign stdout\n",
                stderr=b"companion sign stderr\n",
            ),
            candidate.subprocess.CompletedProcess(
                ["apksigner", "verify", "app"],
                0,
                stdout=f"certificate SHA-256 digest: {digest}\n".encode(),
                stderr=b"",
            ),
            candidate.subprocess.CompletedProcess(
                ["apksigner", "verify", "companion"],
                0,
                stdout=f"certificate SHA-256 digest: {digest}\n".encode(),
                stderr=b"companion verify stderr\n",
            ),
        ]
        stdout = io.BytesIO()
        stderr = io.BytesIO()
        stdout_stream = io.TextIOWrapper(stdout, encoding="utf-8", write_through=True)
        stderr_stream = io.TextIOWrapper(stderr, encoding="utf-8", write_through=True)

        with (
            mock.patch.object(candidate, "_android_tool", return_value=Path("/keytool")),
            mock.patch.object(candidate, "_android_apksigner", return_value=Path("/apksigner")),
            mock.patch.object(candidate.subprocess, "run", side_effect=completed),
            mock.patch.object(candidate.sys, "stdout", stdout_stream),
            mock.patch.object(candidate.sys, "stderr", stderr_stream),
        ):
            candidate._sign_android_pair(
                unsigned_app,
                unsigned_companion,
                signed_app,
                signed_companion,
            )

        self.assertIn(b"keytool stderr\n", stderr.getvalue())
        self.assertIn(b"app sign stdout\n", stdout.getvalue())
        self.assertIn(b"app sign stderr\n", stderr.getvalue())
        self.assertIn(b"companion sign stdout\n", stdout.getvalue())
        self.assertIn(b"companion sign stderr\n", stderr.getvalue())
        self.assertIn(f"certificate SHA-256 digest: {digest}\n".encode(), stdout.getvalue())
        self.assertIn(b"companion verify stderr\n", stderr.getvalue())
        metadata = stderr.getvalue().decode("utf-8")
        self.assertIn('"event":"start"', metadata)
        self.assertIn('"event":"finish"', metadata)
        self.assertIn('"stdout_bytes":0', metadata)
        self.assertIn('"stderr_bytes":0', metadata)
        self.assertIn('"label":"could not sign the local Android qualification APK"', metadata)
        self.assertIn('"argv":["/keytool"', metadata)

    def test_desktop_adapter_builds_native_service_cli_and_ui_test(self) -> None:
        helper = self.source / ".github" / "scripts" / "desktop_build.py"
        helper.parent.mkdir(parents=True)
        helper.write_text("# fixture")
        commands: list[list[str]] = []
        environments: list[dict[str, str] | None] = []

        def capture(
            command: list[str],
            *,
            source_root: Path,
            environment: dict[str, str] | None = None,
        ) -> None:
            self.assertEqual(source_root, self.source)
            commands.append(command)
            environments.append(environment)

        with mock.patch.object(candidate, "_run", side_effect=capture):
            candidate._build_desktop(
                self.source,
                "windows",
                "amd64",
                True,
            )
        self.assertEqual(len(commands), 3)
        self.assertEqual(commands[0][1:4], [str(helper), "libs", "--platform"])
        self.assertIn("--with-cli", commands[0])
        self.assertIn("--skip-deps", commands[0])
        self.assertEqual(commands[1][1:4], [str(helper), "ui-test", "--platform"])
        self.assertIn("--skip-deps", commands[1])
        self.assertEqual(commands[2][1:4], [str(helper), "ui", "--platform"])
        self.assertIn("--skip-deps", commands[2])
        self.assertEqual(len(environments), 3)
        self.assertNotIn("GRADLE_USER_HOME", environments[0] or {})

    def test_linux_adapter_builds_service_and_cli_only(self) -> None:
        helper = self.source / ".github" / "scripts" / "desktop_build.py"
        helper.parent.mkdir(parents=True)
        helper.write_text("# fixture")
        commands: list[list[str]] = []

        with mock.patch.object(
            candidate,
            "_run",
            side_effect=lambda command, **_kwargs: commands.append(command),
        ):
            candidate._build_desktop(
                self.source,
                "linux",
                "amd64",
                True,
            )

        self.assertEqual(len(commands), 1)
        self.assertEqual(commands[0][1:4], [str(helper), "libs", "--platform"])
        self.assertIn("--with-cli", commands[0])

if __name__ == "__main__":
    unittest.main()
