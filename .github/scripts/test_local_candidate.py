from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import stat
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
        app = self.source / "kmp_module" / "app" / "build" / "compose" / "jars"
        app.mkdir(parents=True)
        (app / "app-jvm-1.0.jar").write_bytes(b"app")

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
                self.assertEqual(descriptor["kind"], candidate.KIND)
                self.assertEqual(descriptor["platform"], platform)
                self.assertNotIn("source_sha", descriptor)
                self.assertNotIn("profile", json.dumps(descriptor))
                self.assertNotIn("provider", json.dumps(descriptor))
                self.assertEqual(json.loads(output.read_text()), descriptor)
                self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o600)
                for interface in ("cli", "service", "app"):
                    value = descriptor["interfaces"][interface]
                    self.assertIsNotNone(value)
                    self.assertTrue(Path(value["path"]).is_relative_to(self.request))
                self.assertEqual(
                    descriptor["interfaces"]["network"]["path"],
                    str(self.source / ".dobbyvpn-run" / "s"),
                )
                self.assertEqual(
                    stat.S_IMODE((self.source / ".dobbyvpn-run").stat().st_mode),
                    0o711,
                )

    def test_prepare_describes_android_without_source_identity(self) -> None:
        apk = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-release-unsigned.apk"
        companion = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-test-companion.apk"

        def fake_android(source: Path, root: Path, architecture: str, source_sha: str | None) -> Path:
            self.assertEqual(source, self.source)
            self.assertEqual(architecture, "arm64-v8a")
            self.assertIsNone(source_sha)
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
        self.assertIsNone(descriptor["interfaces"]["cli"])
        self.assertIsNone(descriptor["interfaces"]["service"])
        self.assertEqual(descriptor["interfaces"]["app"]["process_identity"], "com.dobby.vpn")
        self.assertEqual(
            descriptor["test_companion"]["process_identity"],
            "com.dobby.vpn.test",
        )
        self.assertTrue(Path(descriptor["test_companion"]["path"]).is_relative_to(self.request))
        self.assertNotIn("source_sha", json.loads((self.request / "android.json").read_text()))
        self.assertEqual(stat.S_IMODE(apk.parent.stat().st_mode), 0o711)
        self.assertEqual(stat.S_IMODE(apk.stat().st_mode), 0o444)
        self.assertEqual(stat.S_IMODE(companion.stat().st_mode), 0o444)

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

        self.assertEqual(descriptor["interfaces"]["logs"], {
            "app_path": str(app_log),
            "service_path": str(service_log),
        })
        self.assertFalse((self.source / ".dobbyvpn-local-candidate" / "logs").exists())
        self.assertEqual(stat.S_IMODE(app_log.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(service_log.stat().st_mode), 0o600)
        self.assertEqual(app_log.read_bytes(), b"existing app log\n")
        self.assertEqual(service_log.read_bytes(), b"existing service log\n")

    def test_android_interface_exposure_reports_permission_failure(self) -> None:
        candidate_root = self.source / ".dobbyvpn-local-candidate"
        candidate_root.mkdir()
        app = candidate_root / "dobbyvpn-release.apk"
        companion = candidate_root / "dobbyvpn-test-companion.apk"
        app.write_bytes(b"app")
        companion.write_bytes(b"companion")

        with mock.patch.object(Path, "chmod", side_effect=OSError("denied")):
            with self.assertRaisesRegex(
                candidate.CandidateError,
                "could not expose Android candidate interfaces",
            ):
                candidate._expose_android_interfaces(
                    self.request,
                    candidate_root,
                    app,
                    companion,
                )

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

    def test_regular_file_rejects_hardlinks(self) -> None:
        original = self.request / "original"
        linked = self.request / "linked"
        original.write_bytes(b"artifact")
        linked.hardlink_to(original)

        with self.assertRaisesRegex(
            candidate.CandidateError,
            "must be a single-link regular non-symlink file",
        ):
            candidate._regular_file(linked, self.request, "artifact")

    def test_android_source_identity_is_forwarded_only_when_supplied(self) -> None:
        source_sha = "a" * 40
        apk = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-release-unsigned.apk"
        companion = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-test-companion.apk"

        def fake_android(_source: Path, root: Path, _architecture: str, supplied_sha: str | None) -> Path:
            self.assertEqual(supplied_sha, source_sha)
            apk.parent.mkdir(parents=True, exist_ok=True)
            apk.write_bytes(b"apk")
            companion.write_bytes(b"companion")
            return apk

        with mock.patch.object(candidate, "_build_android", side_effect=fake_android):
            candidate.prepare_candidate(
                request_root=self.request,
                source_root=self.source,
                platform="android",
                source_sha=source_sha,
                output=self.request / "android.json",
            )

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
            result = candidate._build_android(self.source, candidate_root, "arm64-v8a", None)

        self.assertEqual(result, signed)
        self.assertEqual(len(commands), 1)
        self.assertIn("--allow-dirty-source", commands[0])
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
                candidate._build_android(self.source, candidate_root, "arm64-v8a", None)

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

    def test_descriptor_rejects_outside_paths_and_extra_fields(self) -> None:
        self._desktop_outputs("linux")
        with mock.patch.object(candidate, "_build_desktop"):
            descriptor = candidate.prepare_candidate(
                request_root=self.request,
                source_root=self.source,
                platform="linux",
                output=self.request / "linux.json",
            )
        outside = self.request.parent / "outside-candidate-file"
        outside.write_bytes(b"outside")
        try:
            descriptor["interfaces"]["app"]["path"] = str(outside)
            with self.assertRaises(candidate.CandidateError):
                candidate.validate_descriptor(descriptor, self.request)
            descriptor["interfaces"]["app"]["path"] = str(self.source / "kmp_module/app/build/compose/jars/app-jvm-1.0.jar")
            descriptor["credentials"] = "must not be accepted"
            with self.assertRaises(candidate.CandidateError):
                candidate.validate_descriptor(descriptor, self.request)
        finally:
            outside.unlink()

    def test_desktop_adapter_invokes_existing_build_helper(self) -> None:
        helper = self.source / ".github" / "scripts" / "desktop_build.py"
        helper.parent.mkdir(parents=True)
        helper.write_text("# fixture")
        gradle_home = self.source / ".dobbyvpn-local-candidate" / "gradle-home"
        gradle_home.mkdir(parents=True)
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
                "linux",
                "amd64",
                True,
                None,
                gradle_home,
            )
        self.assertEqual(len(commands), 2)
        self.assertEqual(commands[0][1:4], [str(helper), "libs", "--platform"])
        self.assertIn("--skip-libs", commands[1])
        self.assertIn("--skip-deps", commands[0])
        self.assertEqual(
            [environment["GRADLE_USER_HOME"] for environment in environments if environment],
            [str(gradle_home), str(gradle_home)],
        )

    def test_prepare_confines_desktop_gradle_home_to_candidate_state(self) -> None:
        self._desktop_outputs("linux")
        captured: list[Path] = []

        def build(
            _source: Path,
            _platform: str,
            _architecture: str,
            _skip_deps: bool,
            _gradle_bin: Path | None,
            gradle_home: Path,
        ) -> None:
            captured.append(gradle_home)

        with mock.patch.object(candidate, "_build_desktop", side_effect=build):
            candidate.prepare_candidate(
                request_root=self.request,
                source_root=self.source,
                platform="linux",
                output=self.request / "linux.json",
            )

        expected = self.source / ".dobbyvpn-local-candidate" / "gradle-home"
        self.assertEqual(captured, [expected])
        self.assertTrue(expected.is_dir())
        self.assertTrue(expected.is_relative_to(self.request))
        self.assertEqual(stat.S_IMODE(expected.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(expected.parent.stat().st_mode), 0o711)

    def test_desktop_candidate_root_exposure_reports_permission_failure(self) -> None:
        self._desktop_outputs("linux")

        with mock.patch.object(candidate, "_build_desktop"), mock.patch.object(
            candidate, "_expose_candidate_root", side_effect=candidate.CandidateError(
                "could not expose candidate root"
            )
        ):
            with self.assertRaisesRegex(
                candidate.CandidateError,
                "could not expose candidate root",
            ):
                candidate.prepare_candidate(
                    request_root=self.request,
                    source_root=self.source,
                    platform="linux",
                    output=self.request / "linux.json",
                )


if __name__ == "__main__":
    unittest.main()
