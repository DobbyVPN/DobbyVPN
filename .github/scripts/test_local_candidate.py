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

    def test_prepare_describes_android_without_source_identity(self) -> None:
        apk = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-release-unsigned.apk"

        def fake_android(source: Path, root: Path, architecture: str, source_sha: str | None) -> Path:
            self.assertEqual(source, self.source)
            self.assertEqual(architecture, "arm64-v8a")
            self.assertIsNone(source_sha)
            apk.parent.mkdir(parents=True, exist_ok=True)
            apk.write_bytes(b"apk")
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
        self.assertNotIn("source_sha", json.loads((self.request / "android.json").read_text()))

    def test_android_source_identity_is_forwarded_only_when_supplied(self) -> None:
        source_sha = "a" * 40
        apk = self.source / ".dobbyvpn-local-candidate" / "dobbyvpn-release-unsigned.apk"

        def fake_android(_source: Path, root: Path, _architecture: str, supplied_sha: str | None) -> Path:
            self.assertEqual(supplied_sha, source_sha)
            apk.parent.mkdir(parents=True, exist_ok=True)
            apk.write_bytes(b"apk")
            return apk

        with mock.patch.object(candidate, "_build_android", side_effect=fake_android):
            candidate.prepare_candidate(
                request_root=self.request,
                source_root=self.source,
                platform="android",
                source_sha=source_sha,
                output=self.request / "android.json",
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
        commands: list[list[str]] = []
        with mock.patch.object(candidate, "_run", side_effect=lambda command, *, source_root: commands.append(command)):
            candidate._build_desktop(self.source, "linux", "amd64", True, None)
        self.assertEqual(len(commands), 2)
        self.assertEqual(commands[0][1:4], [str(helper), "libs", "--platform"])
        self.assertIn("--skip-libs", commands[1])
        self.assertIn("--skip-deps", commands[0])


if __name__ == "__main__":
    unittest.main()
