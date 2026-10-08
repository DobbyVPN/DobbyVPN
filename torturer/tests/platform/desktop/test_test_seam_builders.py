from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


PRODUCT_ROOT = Path(__file__).resolve().parents[4]
DESKTOP_SCRIPT_DIR = PRODUCT_ROOT / ".github" / "scripts" / "desktop"
ANDROID_SCRIPT_DIR = PRODUCT_ROOT / ".github" / "scripts" / "android"
sys.path.insert(0, str(DESKTOP_SCRIPT_DIR))
sys.path.insert(0, str(ANDROID_SCRIPT_DIR))

import desktop_build  # noqa: E402
import local_candidate  # noqa: E402


class TestSeamBuilderTests(unittest.TestCase):
    def test_desktop_service_builder_targets_native_hosts_and_existing_backend(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / "candidate backend"
            output.write_bytes(b"ordinary backend")
            marker = root / "logs" / "recovery-stop.arm"
            marker.parent.mkdir()

            for platform, arch in (("windows", "native"), ("macos", "arm64")):
                with self.subTest(platform=platform):
                    args = type("Args", (), {
                        "platform": platform,
                        "arch": arch,
                        "skip_deps": True,
                        "go_mod_tidy": False,
                        "output": str(output),
                        "runtime_dir": None,
                        "recovery_stop_marker": str(marker),
                    })()
                    with mock.patch.object(desktop_build, "build_service") as build:
                        desktop_build.build_test_seams_service(args)

                    self.assertEqual(build.call_args.args[0], platform)
                    self.assertEqual(build.call_args.args[1], None if arch == "native" else arch)
                    self.assertEqual(build.call_args.kwargs["build_tags"], ("dobbyvpn_test_seams",))
                    self.assertEqual(build.call_args.kwargs["output_path"], output)
                    self.assertIsNone(build.call_args.kwargs["runtime_dir"])
                    self.assertEqual(build.call_args.kwargs["recovery_stop_marker"], marker)

    def test_recovery_marker_must_be_absolute_and_absent(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / "candidate backend"
            args = type("Args", (), {
                "platform": "windows",
                "arch": "native",
                "skip_deps": True,
                "go_mod_tidy": False,
                "output": str(output),
                "runtime_dir": None,
                "recovery_stop_marker": "relative/recovery-stop.arm",
            })()
            with mock.patch.object(desktop_build, "build_service") as build:
                with self.assertRaisesRegex(SystemExit, "must be an absolute path"):
                    desktop_build.build_test_seams_service(args)
                args.recovery_stop_marker = str(root / "missing-parent" / "recovery-stop.arm")
                with self.assertRaisesRegex(SystemExit, "parent must already exist"):
                    desktop_build.build_test_seams_service(args)
                marker = root / "recovery-stop.arm"
                marker.touch()
                args.recovery_stop_marker = str(marker)
                with self.assertRaisesRegex(SystemExit, "must not already exist"):
                    desktop_build.build_test_seams_service(args)
            build.assert_not_called()

    def test_linux_service_requires_runtime_directory_and_output_can_be_replaced(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / "backend"
            output.write_bytes(b"ordinary backend")
            args = type("Args", (), {
                "platform": "linux",
                "arch": "amd64",
                "skip_deps": True,
                "go_mod_tidy": False,
                "output": str(output),
                "runtime_dir": None,
                "recovery_stop_marker": None,
            })()
            with mock.patch.object(desktop_build, "build_service") as build:
                with self.assertRaisesRegex(SystemExit, "--runtime-dir is required"):
                    desktop_build.build_test_seams_service(args)
                args.runtime_dir = temporary
                desktop_build.build_test_seams_service(args)

            self.assertEqual(build.call_args.args[0], "linux")
            self.assertEqual(build.call_args.kwargs["output_path"], output)
            self.assertEqual(build.call_args.kwargs["runtime_dir"], Path(temporary))

    def test_linker_marker_value_quotes_paths_with_spaces(self) -> None:
        marker = "core/sessionapi/runtime.TestRecoveryStopMarker=/run/session logs/recovery-stop.arm"
        self.assertEqual(desktop_build._quote_go_ldflags_token(marker), f"'{marker}'")

    def test_service_build_embeds_the_exact_marker_in_ldflags(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            runtime_dir = root / "runtime"
            runtime_dir.mkdir()
            output = root / "candidate backend"
            output.write_bytes(b"ordinary backend")
            marker = root / "run logs" / "recovery-stop.arm"
            marker.parent.mkdir()
            commands: list[list[str]] = []

            def fake_build(command: list[str], **_: object) -> None:
                commands.append(command)
                Path(command[command.index("-o") + 1]).write_bytes(b"tagged backend")

            with (
                mock.patch.object(desktop_build, "go_mod_download"),
                mock.patch.object(desktop_build, "go_build_identity", return_value="-buildid="),
                mock.patch.object(desktop_build, "run", side_effect=fake_build),
            ):
                desktop_build.build_service(
                    "linux",
                    "amd64",
                    True,
                    False,
                    False,
                    go_executable=Path("/selected/go"),
                    build_tags=("dobbyvpn_test_seams",),
                    output_path=output,
                    runtime_dir=runtime_dir,
                    recovery_stop_marker=marker,
                )

            ldflags = next(argument for argument in commands[0] if argument.startswith("-ldflags="))
            self.assertIn(
                f"-X 'core/sessionapi/runtime.TestRecoveryStopMarker={marker}'",
                ldflags,
            )
            self.assertEqual(output.read_bytes(), b"tagged backend")

    def test_android_candidate_forwards_only_explicit_local_test_seams(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            candidate = source / "candidate"
            android_scripts = source / ".github" / "scripts" / "android"
            android_scripts.mkdir(parents=True)
            candidate.mkdir(parents=True)
            (android_scripts / "android_build_driver.sh").write_text("#!/bin/sh\n", encoding="utf-8")
            calls: list[list[str]] = []

            def fake_run(command: list[str], **_: object) -> None:
                calls.append(command)
                for flag in ("--output", "--test-companion-output"):
                    path = Path(command[command.index(flag) + 1])
                    path.write_bytes(b"unsigned APK")

            def fake_sign_pair(
                app: Path, companion: Path, signed_app: Path, signed_companion: Path, **_: object,
            ) -> None:
                signed_app.write_bytes(app.read_bytes())
                signed_companion.write_bytes(companion.read_bytes())

            with (
                mock.patch.object(local_candidate, "_run", side_effect=fake_run),
                mock.patch.object(local_candidate, "sign_test_pair", side_effect=fake_sign_pair),
                mock.patch.object(local_candidate, "find_android_tool", return_value=Path("tool")),
            ):
                local_candidate._build_android(
                    source,
                    candidate,
                    "arm64-v8a",
                    source_sha=None,
                    source_tree=None,
                    test_seams=True,
                )
                local_candidate._build_android(
                    source,
                    candidate,
                    "arm64-v8a",
                    source_sha=None,
                    source_tree=None,
                )

            self.assertIn("--test-seams", calls[0])
            self.assertEqual(calls[0][0], str(source / ".github" / "scripts" / "android" / "android_build_check.sh"))
            self.assertNotIn("--test-seams", calls[1])

    def test_android_test_seams_reject_archived_source_identity(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            request = Path(temporary)
            source = request / "source"
            source.mkdir()
            identity = "a" * 40
            with self.assertRaisesRegex(local_candidate.CandidateError, "cannot use archived source identities"):
                local_candidate.prepare_candidate(
                    request_root=request,
                    source_root=source,
                    platform="android",
                    source_sha=identity,
                    source_tree=identity,
                    test_seams=True,
                )


if __name__ == "__main__":
    unittest.main()
