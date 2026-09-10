from __future__ import annotations

import importlib.util
import io
import os
from pathlib import Path
import plistlib
import stat
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("desktop_build.py")
SPEC = importlib.util.spec_from_file_location("desktop_build", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT_PATH}")
desktop_build = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = desktop_build
SPEC.loader.exec_module(desktop_build)


class DesktopBuildTests(unittest.TestCase):
    def test_go_root_requires_the_standard_library_tree(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "bin").mkdir()
            (root / "bin" / "go.exe").write_bytes(b"go")
            (root / "go" / "src" / "runtime").mkdir(parents=True)
            with mock.patch.object(desktop_build, "host_platform", return_value="windows"):
                self.assertFalse(desktop_build.go_root_is_complete(root))
                (root / "src" / "runtime").mkdir(parents=True)
                self.assertTrue(desktop_build.go_root_is_complete(root))

    def test_find_go_skips_a_malformed_cached_installation(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "go-1.25.1"
            (root / "bin").mkdir(parents=True)
            (root / "bin" / "go.exe").write_bytes(b"go")
            with (
                mock.patch.object(desktop_build, "ROOT_DIR", Path(temporary)),
                mock.patch.object(desktop_build, "TOOLS_DIR", Path(temporary)),
                mock.patch.object(desktop_build, "host_platform", return_value="windows"),
                mock.patch.object(desktop_build.shutil, "which", return_value=str(root / "bin" / "go.exe")),
                mock.patch.object(desktop_build, "run_capture") as run_capture,
            ):
                (Path(temporary) / ".go-version").write_text("1.25.1\n", encoding="utf-8")
                self.assertIsNone(desktop_build.find_go())
            run_capture.assert_not_called()

    def test_literal_config_uses_fresh_owner_only_file(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixed = root / "cli-test-config.toml"
            fixed.write_text("prior config\n", encoding="utf-8")
            with mock.patch.object(desktop_build, "ROOT_DIR", root):
                path = Path(desktop_build.prepare_config_arg("literal config\n"))
            try:
                self.assertNotEqual(path, fixed)
                self.assertEqual(path.read_text(encoding="utf-8"), "literal config\n")
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertEqual(fixed.read_text(encoding="utf-8"), "prior config\n")
            finally:
                path.unlink(missing_ok=True)

    def test_macos_package_bundle_and_launch_service_names_are_consistent(self) -> None:
        repository = SCRIPT_PATH.parents[2]
        installer = repository / "installer" / "macos"
        build_script = (installer / "build.sh").read_text(encoding="utf-8")
        postinstall = (installer / "postinstall.sh").read_text(encoding="utf-8")
        with (installer / "vpnservice.plist").open("rb") as source:
            service = plistlib.load(source)

        self.assertIn('EXTRACTED_APP_BUNDLE="Dobby Vpn.app"', build_script)
        self.assertIn('APP_BUNDLE="Dobby VPN.app"', build_script)
        self.assertEqual(build_script.count('mv "$EXTRACTED_APP_BUNDLE" "$APP_BUNDLE"'), 2)
        self.assertNotIn("pkgbuild --analyze", build_script)
        self.assertIn("write_fixed_payload_component_plist()", build_script)
        self.assertEqual(build_script.count("write_fixed_payload_component_plist"), 3)
        self.assertIn('<plist version="1.0"><array/></plist>', build_script)
        self.assertEqual(build_script.count("--component-plist component.plist"), 2)
        self.assertNotIn('/Applications/Dobby Vpn.app', postinstall)
        self.assertIn('/Applications/Dobby VPN.app', postinstall)
        self.assertEqual(service["Label"], "com.dobby.vpnservice")
        self.assertEqual(service["UserName"], "root")
        self.assertEqual(
            service["ProgramArguments"][0],
            "/Applications/Dobby VPN.app/Contents/Resources/macos_grpcvpnserver",
        )
        self.assertEqual(
            service["WorkingDirectory"],
            "/Applications/Dobby VPN.app/Contents/Resources/",
        )

    def test_macos_installer_preserves_setup_failure_before_daemon_start(self) -> None:
        # A failed chmod used to be hidden by the later launchctl exit status.
        script = SCRIPT_PATH.parents[2] / "installer/macos/postinstall.sh"
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            for name, body in {
                "stat": "printf 'desktop-user\\n'",
                "id": "printf '501\\n'",
                "chmod": "echo 'fixture chmod failure' >&2; exit 47",
                "launchctl": "echo 'daemon must not start'; exit 0",
            }.items():
                executable = directory / name
                executable.write_text("#!/bin/sh\n" + body + "\n", encoding="utf-8")
                executable.chmod(0o755)
            completed = subprocess.run(
                ["/bin/bash", str(script)],
                env={**os.environ, "PATH": str(directory)},
                capture_output=True, check=False,
            )
        self.assertEqual(completed.returncode, 47)
        self.assertEqual(completed.stdout, b"")
        self.assertEqual(completed.stderr, b"fixture chmod failure\n")

    def test_builds_do_not_package_removed_cloak_runtime(self) -> None:
        script = SCRIPT_PATH.read_text(encoding="utf-8")
        android = (SCRIPT_PATH.parents[2] / "kmp_module" / "app" / "build.gradle.kts").read_text(encoding="utf-8")
        ios = (SCRIPT_PATH.parents[1] / "workflows" / "ios_libs_generate.yml").read_text(encoding="utf-8")
        desktop = (SCRIPT_PATH.parents[1] / "workflows" / "desktop_libs_generate.yml").read_text(encoding="utf-8")

        self.assertNotIn("Using tracked embedded Cloak client source", script)
        self.assertNotIn("validate_embedded_cloak_source", script)
        self.assertNotIn("copytree(source_dir, target_dir", script)
        self.assertNotIn("from(cloakInternalDir)", android)
        self.assertNotIn("inputs.dir(goModuleCloakInternalDir)", android)
        self.assertNotIn("cp -r Cloak/internal", ios)
        self.assertNotIn("'Cloak/internal/**'", ios)
        self.assertNotIn("'Cloak/internal/**'", desktop)

    def test_curl_download_has_bounded_transfer_time_without_retries(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_name:
            output = Path(temporary_name) / "download.bin"
            with (
                mock.patch.object(desktop_build.shutil, "which", return_value="curl"),
                mock.patch.object(desktop_build, "run") as run,
            ):
                desktop_build.download("https://example.invalid/download.bin", output)

        command = run.call_args.args[0]
        self.assertEqual(command[command.index("--connect-timeout") + 1], "60")
        self.assertEqual(command[command.index("--max-time") + 1], "900")
        self.assertNotIn("--retry", command)

    def test_windows_compiler_probe_resolves_path_and_requires_exact_target(self) -> None:
        compiler = Path("C:/tools/mingw64/bin/gcc.exe")
        completed = mock.Mock(returncode=0, stdout="x86_64-w64-mingw32\n", stderr="compiler warning\n")

        with (
            mock.patch.object(desktop_build.shutil, "which", return_value="C:/links/gcc.exe"),
            mock.patch.object(desktop_build.Path, "resolve", return_value=compiler),
            mock.patch.object(desktop_build, "prepend_path") as prepend_path,
            mock.patch.object(desktop_build, "run_bounded_capture", return_value=completed) as run,
        ):
            usable, diagnostic = desktop_build.probe_windows_gcc()

        self.assertTrue(usable)
        self.assertEqual(diagnostic, "compiler=ready target=x86_64-w64-mingw32")
        prepend_path.assert_called_once_with(compiler.parent)
        self.assertEqual(run.call_args.args[0], [str(compiler), "-dumpmachine"])

    def test_windows_compiler_rejects_present_but_broken_gcc_with_skip_deps(self) -> None:
        with (
            mock.patch.object(desktop_build, "prepend_path"),
            mock.patch.object(
                desktop_build,
                "probe_windows_gcc",
                return_value=(False, "compiler=unusable exit_code=-1073741515 target=invalid"),
            ),
        ):
            with self.assertRaisesRegex(SystemExit, "exit_code=-1073741515"):
                desktop_build.ensure_compiler("windows", True)

    def test_windows_compiler_probe_rejects_whitespace_in_resolved_toolchain_path(self) -> None:
        compiler = Path("C:/Program Files/WinGet/Packages/mingw64/bin/gcc.exe")

        with (
            mock.patch.object(desktop_build.shutil, "which", return_value="C:/links/gcc.exe"),
            mock.patch.object(desktop_build.Path, "resolve", return_value=compiler),
            mock.patch.object(desktop_build, "prepend_path") as prepend_path,
            mock.patch.object(desktop_build.subprocess, "run") as run,
        ):
            usable, diagnostic = desktop_build.probe_windows_gcc()

        self.assertFalse(usable)
        self.assertEqual(diagnostic, "compiler=unsupported_path_contains_whitespace")
        prepend_path.assert_not_called()
        run.assert_not_called()

    def test_failed_probe_output_is_emitted_without_being_suppressed(self) -> None:
        completed = mock.Mock(returncode=7, stdout="probe stdout\nprobe stderr\n", stderr=None)
        diagnostics = io.StringIO()
        with (
            mock.patch.object(desktop_build, "run_bounded_capture", return_value=completed),
            mock.patch.object(desktop_build.sys, "stderr", diagnostics),
        ):
            self.assertIsNone(desktop_build.run_capture(["probe", "version"]))
        self.assertIn("exit code 7", diagnostics.getvalue())
        self.assertIn("probe stdout\nprobe stderr\n", diagnostics.getvalue())

    def test_failed_windows_compiler_probe_preserves_child_output(self) -> None:
        compiler = Path("C:/tools/mingw64/bin/gcc.exe")
        completed = mock.Mock(returncode=2, stdout="compiler stdout\n", stderr="compiler stderr\n")
        diagnostics = io.StringIO()
        with (
            mock.patch.object(desktop_build.shutil, "which", return_value="C:/links/gcc.exe"),
            mock.patch.object(desktop_build.Path, "resolve", return_value=compiler),
            mock.patch.object(desktop_build, "prepend_path"),
            mock.patch.object(desktop_build, "run_bounded_capture", return_value=completed),
            mock.patch.object(desktop_build.sys, "stderr", diagnostics),
        ):
            usable, _ = desktop_build.probe_windows_gcc()
        self.assertFalse(usable)
        self.assertIn("compiler stdout\n", diagnostics.getvalue())
        self.assertIn("compiler stderr\n", diagnostics.getvalue())

    def test_run_scopes_async_preemption_workaround_to_windows_go_children(self) -> None:
        commands = [
            (["go", "version"], "windows", "parent=1", "parent=1,asyncpreemptoff=1"),
            (["C:/Go/bin/go.exe", "version"], "windows", "parent=2", "parent=2,asyncpreemptoff=1"),
            (["powershell", "-Command", "build"], "windows", "parent=3", "parent=3"),
            (["go", "version"], "linux", "parent=4", "parent=4"),
        ]

        with (
            mock.patch.object(desktop_build, "log"),
            mock.patch.object(desktop_build, "host_platform", side_effect=[item[1] for item in commands]),
            mock.patch.object(
                desktop_build.subprocess,
                "run",
                return_value=subprocess.CompletedProcess([], 0),
            ) as subprocess_run,
        ):
            for command, _, godebug, expected_godebug in commands:
                caller_env = {"GODEBUG": godebug, "KEEP_CALLER_VALUE": "yes"}
                desktop_build.run(command, env=caller_env)
                child_env = subprocess_run.call_args.kwargs["env"]
                self.assertEqual(child_env["GODEBUG"], expected_godebug)
                self.assertEqual(child_env["KEEP_CALLER_VALUE"], "yes")
                self.assertEqual(caller_env["GODEBUG"], godebug)

    def test_bounded_probe_scopes_async_preemption_workaround_to_windows_go(self) -> None:
        process = mock.Mock(pid=123, returncode=0)
        process.communicate.return_value = (b"go version go1.25.1 windows/amd64\n", b"")
        with (
            mock.patch.object(desktop_build, "host_platform", return_value="windows"),
            mock.patch.dict(desktop_build.os.environ, {"GODEBUG": "parent=1"}),
            mock.patch.object(
                desktop_build.subprocess, "Popen", return_value=process,
            ) as popen,
        ):
            result = desktop_build.run_bounded_capture(["go.exe", "version"])
            self.assertEqual(desktop_build.os.environ["GODEBUG"], "parent=1")

        self.assertEqual(result.returncode, 0)
        self.assertEqual(
            popen.call_args.kwargs["env"]["GODEBUG"],
            "parent=1,asyncpreemptoff=1",
        )

    def test_windows_compiler_repairs_broken_gcc_before_returning(self) -> None:
        with (
            mock.patch.object(desktop_build, "prepend_path") as prepend_path,
            mock.patch.object(
                desktop_build,
                "probe_windows_gcc",
                side_effect=[
                    (False, "compiler=unusable exit_code=-1073741515 target=invalid"),
                    (True, "compiler=ready target=x86_64-w64-mingw32"),
                ],
            ),
            mock.patch.object(desktop_build, "command_exists", return_value=True),
            mock.patch.object(desktop_build, "run") as run,
        ):
            desktop_build.ensure_compiler("windows", False)

        self.assertEqual(prepend_path.call_count, 3)
        self.assertEqual(prepend_path.call_args_list[1], mock.call(Path("C:/mingw64/bin")))
        run.assert_called_once_with(["choco", "install", "mingw", "-y"])

    def test_windows_compiler_fails_if_repair_is_still_unusable(self) -> None:
        with (
            mock.patch.object(desktop_build, "prepend_path"),
            mock.patch.object(
                desktop_build,
                "probe_windows_gcc",
                side_effect=[
                    (False, "compiler=missing"),
                    (False, "compiler=unusable exit_code=2 target=invalid"),
                ],
            ),
            mock.patch.object(desktop_build, "command_exists", return_value=True),
            mock.patch.object(desktop_build, "run"),
        ):
            with self.assertRaisesRegex(SystemExit, "remained unusable.*exit_code=2"):
                desktop_build.ensure_compiler("windows", False)

    def test_desktop_package_version_is_required_from_release_inputs(self) -> None:
        conveyor = (SCRIPT_PATH.parents[2] / "kmp_module" / "conveyor.conf").read_text(
            encoding="utf-8",
        )
        self.assertIn(
            "app.version = ${env.APP_MAJOR_VERSION}.${env.APP_MINOR_VERSION}.${env.APP_MAINTENANCE_VERSION}",
            conveyor,
        )
        self.assertNotIn("app.version = 1.1", conveyor)
        self.assertIn('include "#!../.github/scripts/conveyor-config"', conveyor)

        unix_launcher = SCRIPT_PATH.with_name("conveyor-config")
        windows_launcher = SCRIPT_PATH.with_name("conveyor-config.bat")
        self.assertEqual(
            unix_launcher.read_text(encoding="utf-8"),
            '#!/bin/sh\nexec python3 "$(dirname "$0")/desktop_build.py" conveyor-config\n',
        )
        self.assertTrue(unix_launcher.stat().st_mode & stat.S_IXUSR)
        self.assertEqual(
            windows_launcher.read_text(encoding="utf-8"),
            "@echo off\n"
            'python.exe "%~dp0desktop_build.py" conveyor-config\n'
            "exit /b %ERRORLEVEL%\n",
        )

        workflow = (SCRIPT_PATH.parents[1] / "workflows" / "desktop_build.yml").read_text(
            encoding="utf-8",
        )
        for name in (
            "APP_MAJOR_VERSION",
            "APP_MINOR_VERSION",
            "APP_MAINTENANCE_VERSION",
        ):
            self.assertIn(f"{name}: ${{{{ inputs.{name.lower()} }}}}", workflow)
        self.assertIn('test "$(dpkg-deb -f "$deb_file" Version)" = "$EXPECTED_VERSION"', workflow)
        self.assertNotIn("find ./output -name '*.deb'", workflow)

        installers = (SCRIPT_PATH.parents[1] / "workflows" / "installers_build.yml").read_text(
            encoding="utf-8",
        )
        self.assertIn("Verify Windows installer version", installers)
        self.assertIn("WHERE Property = 'ProductVersion'", installers)
        self.assertIn("MSI ProductVersion $actual does not match $env:EXPECTED_VERSION", installers)
        self.assertIn("Verify macOS installer versions", installers)
        self.assertIn('pkgutil --expand-full "$package" "$expanded"', installers)
        self.assertIn('if actual != expected:', installers)

    def test_gradle_command_keeps_standalone_wrapper_default(self) -> None:
        with mock.patch.object(desktop_build, "host_platform", return_value="macos"):
            self.assertEqual(desktop_build.gradle_command(), "./gradlew")

    def test_desktop_gradle_accepts_a_fixed_absolute_executable_as_one_argv_item(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            fixed = Path(temporary) / "Gradle 8.13" / "bin" / "gradle"
            fixed.parent.mkdir(parents=True)
            fixed.write_text("#!/bin/sh\n", encoding="utf-8")
            fixed.chmod(0o755)
            with (
                mock.patch.object(desktop_build, "install_jdk"),
                mock.patch.object(desktop_build, "install_android_sdk"),
                mock.patch.object(
                    desktop_build,
                    "desktop_version_properties",
                    return_value=["-PversionName=1.4.7"],
                ),
                mock.patch.object(desktop_build, "run") as run,
            ):
                desktop_build.run_desktop_gradle(True, fixed)

        self.assertEqual(len(run.call_args_list), 3)
        for call in run.call_args_list:
            self.assertEqual(call.args[0][0], str(fixed))
            self.assertIs(call.kwargs["cwd"], desktop_build.KMP_DIR)

    def test_fixed_gradle_executable_uses_the_supplied_path(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixed = root / "gradle"
            fixed.write_text("#!/bin/sh\n", encoding="utf-8")
            fixed.chmod(0o755)
            link = root / "gradle-link"
            link.symlink_to(fixed)

            self.assertEqual(desktop_build.gradle_command("gradle"), "gradle")
            self.assertEqual(desktop_build.gradle_command(link), str(link))

    def test_conveyor_config_uses_host_gradle_wrapper_and_emits_only_hocon(self) -> None:
        completed = mock.Mock(returncode=0, stdout="// Generated by the Conveyor Gradle plugin.\napp.display-name = DobbyVPN\n")
        output = io.StringIO()
        diagnostics = io.StringIO()
        with (
            mock.patch.object(
                desktop_build,
                "install_jdk",
                side_effect=lambda **_kwargs: print("jdk-ready"),
            ) as install_jdk,
            mock.patch.object(desktop_build, "gradle_command", return_value="gradlew.bat"),
            mock.patch.object(desktop_build, "desktop_version_properties", return_value=["-PversionName=1.4.7"]),
            mock.patch.object(desktop_build.subprocess, "run", return_value=completed) as run,
            mock.patch.object(desktop_build.sys, "stdout", output),
            mock.patch.object(desktop_build.sys, "stderr", diagnostics),
        ):
            desktop_build.emit_conveyor_config()

        install_jdk.assert_called_once_with(skip_deps=False)
        self.assertIn("app.display-name = DobbyVPN", output.getvalue())
        self.assertIn("jdk-ready\n", diagnostics.getvalue())
        self.assertIn("// Generated by the Conveyor Gradle plugin.", diagnostics.getvalue())
        self.assertEqual(
            run.call_args.args[0],
            ["gradlew.bat", "--no-daemon", "printConveyorConfig", "-PversionName=1.4.7"],
        )
        self.assertEqual(run.call_args.kwargs["cwd"], str(desktop_build.KMP_DIR))
        self.assertIs(run.call_args.kwargs["stderr"], diagnostics)

    def test_desktop_gradle_invocations_are_all_non_daemon(self) -> None:
        props = ["-PversionName=1.5.0"]
        with (
            mock.patch.object(desktop_build, "install_jdk"),
            mock.patch.object(desktop_build, "install_android_sdk"),
            mock.patch.object(desktop_build, "desktop_version_properties", return_value=props),
            mock.patch.object(desktop_build, "gradle_command", return_value="gradlew"),
            mock.patch.object(desktop_build, "run") as run,
        ):
            desktop_build.run_desktop_gradle(skip_deps=True)

        self.assertEqual(
            run.call_args_list,
            [
                mock.call(
                    ["gradlew", "--no-daemon", "--build-cache", "--parallel", ":app:jvmJar", *props],
                    cwd=desktop_build.KMP_DIR,
                ),
                mock.call(
                    ["gradlew", "--no-daemon", "dependencies", *props],
                    cwd=desktop_build.KMP_DIR,
                ),
                mock.call(
                    ["gradlew", "--no-daemon", "printConveyorConfig", *props],
                    cwd=desktop_build.KMP_DIR,
                ),
            ],
        )
        for invocation in run.call_args_list:
            self.assertIn("--no-daemon", invocation.args[0])

    def test_conveyor_config_failure_never_emits_partial_hocon(self) -> None:
        output = io.StringIO()
        diagnostics = io.StringIO()
        with (
            mock.patch.object(desktop_build, "install_jdk"),
            mock.patch.object(desktop_build, "gradle_command", return_value="gradlew.bat"),
            mock.patch.object(desktop_build, "desktop_version_properties", return_value=[]),
            mock.patch.object(
                desktop_build.subprocess,
                "run",
                return_value=mock.Mock(returncode=7, stdout="partial-private-config\n"),
            ),
            mock.patch.object(desktop_build.sys, "stdout", output),
            mock.patch.object(desktop_build.sys, "stderr", diagnostics),
        ):
            with self.assertRaisesRegex(SystemExit, "failed with exit code 7"):
                desktop_build.emit_conveyor_config()
        self.assertEqual(output.getvalue(), "")
        self.assertIn("partial-private-config\n", diagnostics.getvalue())

    def test_native_bridge_release_contract_is_single_source_and_exact(self) -> None:
        self.assertEqual(
            desktop_build.BRIDGE_RELEASES,
            {
                "windows": desktop_build.BridgeRelease(
                    version="1.0.1",
                    asset_name="dobby_bridge-windows-x86_64.zip",
                    archive_sha256="a7e64db0568547d395bc45e33787f22c7303dca6f5c575c84439e73a70124331",
                    member_name="dobby_bridge.dll",
                    member_sha256="10e2f921aaa949060bed936e3c361b0967b2ad8b7a71dd983d36abd94c903063",
                ),
                "linux": desktop_build.BridgeRelease(
                    version="1.0.1",
                    asset_name="libdobby_bridge-linux-x86_64.zip",
                    archive_sha256="67536090d74212a5635739d297f5a78fbabda1966d161b12a16bfe487a8c68b9",
                    member_name="libdobby_bridge.so",
                    member_sha256="2fff96d2631df43168196e222bc2205157d23a2449475364c5b135fbf0aaa0ce",
                ),
            },
        )

        workflow = (SCRIPT_PATH.parents[1] / "workflows" / "desktop_libs_generate.yml").read_text(
            encoding="utf-8",
        )
        self.assertNotIn("Download TrustTunnel Windows lib", workflow)
        self.assertNotIn("Download TrustTunnel Linux lib", workflow)
        self.assertNotIn("go-go-tunnel/releases/download/v1.0.", workflow)
        self.assertIn("'.github/scripts/desktop_build.py'", workflow)

    def test_wait_for_socket_accepts_unix_domain_socket(self) -> None:
        path = Path("/tmp/dobbyvpn-test/control.sock")

        with mock.patch.object(
            desktop_build.Path,
            "stat",
            return_value=mock.Mock(st_mode=stat.S_IFSOCK | 0o600),
        ):
            self.assertTrue(desktop_build.wait_for_socket(path, timeout_seconds=1))

    def test_unix_service_uses_private_control_socket_for_readiness(self) -> None:
        process = mock.Mock()
        socket_path = Path("/tmp/dobbyvpn-test/control.sock")
        service_log = mock.Mock()
        service_log.name = "/tmp/service.log"

        with (
            mock.patch.object(desktop_build, "service_target_path", return_value=Path("/tmp/service")),
            mock.patch.object(desktop_build.Path, "exists", return_value=True),
            mock.patch.object(desktop_build, "sudo_prefix", return_value=["sudo"]),
            mock.patch.object(
                desktop_build,
                "open_service_log",
                return_value=service_log,
            ),
            mock.patch.object(desktop_build, "wait_for_socket", return_value=True) as wait_for_socket,
            mock.patch.object(desktop_build, "wait_for_port") as wait_for_port,
            mock.patch.object(desktop_build.subprocess, "Popen", return_value=process) as popen,
        ):
            started, _ = desktop_build.start_service("linux", 50151, socket_path)

        self.assertIs(started, process)
        wait_for_socket.assert_called_once_with(socket_path)
        wait_for_port.assert_not_called()
        command = popen.call_args.args[0]
        self.assertIn(f"DOBBYVPN_CONTROL_SOCKET={socket_path}", command)
        self.assertEqual(
            popen.call_args.kwargs["env"]["DOBBYVPN_CONTROL_SOCKET"],
            str(socket_path),
        )

    def test_service_log_reporting_keeps_complete_output_after_close(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(desktop_build, "ROOT_DIR", root):
                handle = desktop_build.open_service_log()
                handle.write(b"first diagnostic line\nsecond diagnostic line\n")
                output = io.StringIO()
                with mock.patch.object(desktop_build.sys, "stdout", output):
                    desktop_build.print_service_logs([handle])
                handle.close()
        self.assertIn("first diagnostic line\nsecond diagnostic line\n", output.getvalue())

    def test_service_log_reporting_prints_complete_output_in_actions(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(desktop_build, "ROOT_DIR", root), mock.patch.dict(
                os.environ,
                {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": temporary},
                clear=False,
            ):
                handle = desktop_build.open_service_log()
                handle.write(b"private service endpoint /tmp/private.sock\n")
                output = io.StringIO()
                diagnostics = io.StringIO()
                with (
                    mock.patch.object(desktop_build.sys, "stdout", output),
                    mock.patch.object(desktop_build.sys, "stderr", diagnostics),
                ):
                    desktop_build.print_service_logs([handle])
                handle.close()
            self.assertIn("private service endpoint", output.getvalue())
            self.assertEqual(diagnostics.getvalue(), "")

    def test_process_diagnostic_prints_complete_output_in_actions(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            diagnostics = io.StringIO()
            with (
                mock.patch.dict(
                    os.environ,
                    {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": temporary},
                    clear=False,
                ),
                mock.patch.object(desktop_build.sys, "stderr", diagnostics),
            ):
                desktop_build.emit_process_diagnostic("probe failed", b"private stderr\xff\n")
            self.assertIn("probe failed", diagnostics.getvalue())
            self.assertIn("private stderr", diagnostics.getvalue())

    def test_service_stop_delegates_to_process_group_cleanup(self) -> None:
        process = mock.Mock(poll=lambda: None)
        with mock.patch.object(
            desktop_build,
            "terminate_process_group",
            return_value="tree=gone source=test observed_pids=1",
        ) as terminate:
            desktop_build.stop_service(process)
        terminate.assert_called_once_with(process)

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_sigterm_resistant_descendant_is_killed_with_process_group(self) -> None:
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        with tempfile.TemporaryDirectory(prefix="desktop-build-resistant-child-") as temporary:
            root = Path(temporary)
            child_stdout = root / "child.stdout.raw.log"
            child_stderr = root / "child.stderr.raw.log"
            parent_code = (
                "import os,signal,subprocess,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                f"child_stdout=os.fdopen(os.open({str(child_stdout)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child_stderr=os.fdopen(os.open({str(child_stderr)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}], stdout=child_stdout, stderr=child_stderr); "
                "print(child.pid, flush=True); time.sleep(60)"
            )
            process = subprocess.Popen(
                [sys.executable, "-c", parent_code],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                start_new_session=True,
            )
            process._dobby_process_group_id = process.pid  # type: ignore[attr-defined]
            try:
                child_pid = int(process.stdout.readline().strip())
                desktop_build.terminate_process_group(process, grace_seconds=0.1)
                self.assertIsNotNone(process.poll())
                for _ in range(30):
                    try:
                        state = Path(f"/proc/{child_pid}/stat").read_text(encoding="ascii")
                    except (FileNotFoundError, ProcessLookupError):
                        break
                    if state[state.rfind(")") + 2 :].split()[0] == "Z":
                        break
                    time.sleep(0.05)
                else:
                    self.fail("SIGTERM-resistant descendant survived process-group cleanup")
                self.assertEqual(child_stdout.stat().st_mode & 0o777, 0o600)
                self.assertEqual(child_stderr.stat().st_mode & 0o777, 0o600)
            finally:
                if process.poll() is None:
                    desktop_build.terminate_process_group(process, grace_seconds=0.1)
                process.stdout.close()
                process.stderr.close()

    @unittest.skipIf(os.name == "nt", "POSIX process-group assertion")
    def test_bounded_probe_timeout_preserves_streams_and_kills_descendants(self) -> None:
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        parent_code = (
            "import signal,subprocess,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}]); "
            "print('childpid='+str(child.pid), flush=True); "
            "print('probe stderr', file=sys.stderr, flush=True); time.sleep(60)"
        )
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with (
                mock.patch.object(desktop_build, "ROOT_DIR", root),
                mock.patch.object(desktop_build, "PROCESS_CLEANUP_GRACE_SECONDS", 0.1),
            ):
                with self.assertRaises(subprocess.TimeoutExpired) as raised:
                    desktop_build.run_bounded_capture(
                        [sys.executable, "-c", parent_code], cwd=root, timeout_seconds=1,
                    )
            output = raised.exception.stdout or raised.exception.output or ""
            child_pid = int(desktop_build.output_text(output).split("childpid=", 1)[1].splitlines()[0])
            self.assertIn("childpid=", desktop_build.output_text(output))
            self.assertIn("probe stderr", raised.exception.stderr)
            for _ in range(30):
                try:
                    state = Path(f"/proc/{child_pid}/stat").read_text(encoding="ascii")
                except (FileNotFoundError, ProcessLookupError):
                    break
                if state[state.rfind(")") + 2 :].split()[0] == "Z":
                    break
                time.sleep(0.05)
            else:
                self.fail("timed-out probe descendant survived cleanup")

    def test_cli_check_passes_control_socket_to_gradle_process(self) -> None:
        socket_path = Path("/tmp/dobbyvpn-test/control.sock")

        with (
            mock.patch.object(desktop_build, "desktop_version_properties", return_value=[]),
            mock.patch.object(desktop_build, "gradle_command", return_value="gradle"),
            mock.patch.object(desktop_build, "run") as run,
        ):
            desktop_build.run_cli_check("/tmp/config.toml", 50151, socket_path)

        self.assertEqual(
            run.call_args.kwargs["env"]["DOBBYVPN_CONTROL_SOCKET"],
            str(socket_path),
        )

    def test_control_socket_and_parent_are_removed_without_recursive_delete(self) -> None:
        socket_path = Path("/tmp/dobbyvpn-test/service/control.sock")

        with (
            mock.patch.object(desktop_build, "sudo_prefix", return_value=["sudo"]),
            mock.patch.object(
                desktop_build.Path,
                "lstat",
                return_value=mock.Mock(st_mode=stat.S_IFSOCK | 0o600),
            ),
            mock.patch.object(desktop_build, "run") as run,
        ):
            desktop_build.remove_control_socket_parent(socket_path)

        self.assertEqual(
            run.call_args_list,
            [
                mock.call(["sudo", "unlink", str(socket_path)]),
                mock.call(["sudo", "rmdir", str(socket_path.parent)]),
            ],
        )

    def test_append_cgo_ldflags_preserves_existing_flags(self) -> None:
        environment = {"CGO_LDFLAGS": "-L/custom"}

        desktop_build.append_cgo_ldflags(
            environment,
            "-lc++",
            "-framework",
            "SystemConfiguration",
        )

        self.assertEqual(
            environment["CGO_LDFLAGS"],
            "-L/custom -lc++ -framework SystemConfiguration",
        )

    def test_prepare_go_test_dependencies_stages_bridge_runtime_and_environment(self) -> None:
        calls: list[object] = []
        environment_updates: dict[str, str] = {}
        with tempfile.TemporaryDirectory() as temporary:
            runtime = Path(temporary) / "llvm-libcxx"
            runtime.mkdir()
            with (
                mock.patch.object(desktop_build, "host_platform", return_value="linux"),
                mock.patch.object(
                    desktop_build,
                    "ensure_build_dependencies",
                    side_effect=lambda *args, **kwargs: calls.append(("deps", args, kwargs)),
                ),
                mock.patch.object(
                    desktop_build,
                    "install_linux_trusttunnel_bridge",
                    side_effect=lambda skip: calls.append(("bridge", skip)),
                ),
                mock.patch.object(
                    desktop_build,
                    "install_linux_libcxx_runtime",
                    side_effect=lambda skip: calls.append(("libcxx", skip)) or runtime,
                ),
                mock.patch.object(
                    desktop_build,
                    "go_mod_download",
                    side_effect=lambda tidy: calls.append(("modules", tidy)),
                ),
                mock.patch.object(
                    desktop_build,
                    "set_env",
                    side_effect=lambda name, value: environment_updates.__setitem__(name, value),
                ),
                mock.patch.dict(desktop_build.os.environ, {"CGO_LDFLAGS": "-L/custom"}, clear=False),
            ):
                desktop_build.prepare_go_test_dependencies(True, True)

        self.assertEqual(calls[0], ("deps", ("linux", True), {"need_android": False}))
        self.assertEqual(calls[1:], [("bridge", True), ("libcxx", True), ("modules", True)])
        self.assertEqual(environment_updates["CGO_ENABLED"], "1")
        self.assertIn(f"-L{desktop_build.GO_MODULE_DIR}", environment_updates["CGO_LDFLAGS"])
        self.assertIn(f"-L{runtime}", environment_updates["CGO_LDFLAGS"])
        self.assertIn(str(desktop_build.GO_MODULE_DIR), environment_updates["LD_LIBRARY_PATH"])
        self.assertIn(str(runtime), environment_updates["LD_LIBRARY_PATH"])

    def test_local_conveyor_options_precede_make_task(self) -> None:
        with (
            mock.patch.dict(desktop_build.os.environ, {"CONVEYOR_CMD": "/tool/conveyor"}),
            mock.patch.object(desktop_build, "run") as run,
        ):
            desktop_build.run_conveyor("fixture-passphrase")

        self.assertEqual(
            run.call_args.args[0],
            [
                "/tool/conveyor", "-f", str(desktop_build.KMP_DIR / "conveyor.conf"),
                "--passphrase=fixture-passphrase", "make", "site",
            ],
        )

    def test_windows_service_stages_import_derived_runtime_before_build(self) -> None:
        calls: list[str] = []

        with (
            mock.patch.object(desktop_build, "host_platform", return_value="windows"),
            mock.patch.dict(desktop_build.os.environ, {"GODEBUG": "gctrace=1"}),
            mock.patch.object(desktop_build, "ensure_build_dependencies"),
            mock.patch.object(
                desktop_build,
                "install_wintun",
                side_effect=lambda skip_deps: calls.append(f"wintun:{skip_deps}"),
            ),
            mock.patch.object(
                desktop_build,
                "install_windows_bridge",
                side_effect=lambda skip_deps: calls.append(f"bridge:{skip_deps}"),
            ),
            mock.patch.object(desktop_build, "go_mod_download"),
            mock.patch.object(desktop_build, "run", side_effect=lambda *args, **kwargs: calls.append("build")) as run,
            mock.patch.object(desktop_build.shutil, "copyfile"),
            mock.patch.object(desktop_build.Path, "mkdir"),
        ):
            desktop_build.build_service("windows", "amd64", True, False, False)
            self.assertEqual(desktop_build.os.environ["GODEBUG"], "gctrace=1")

        self.assertEqual(calls, ["wintun:True", "bridge:True", "build"])
        self.assertEqual(run.call_args.kwargs["env"]["GODEBUG"], "gctrace=1")

    def test_libs_with_cli_builds_both_native_interfaces(self) -> None:
        args = mock.Mock(
            command="libs",
            platform="linux",
            arch="amd64",
            skip_deps=True,
            skip_build=False,
            go_mod_tidy=False,
            with_cli=True,
        )
        with (
            mock.patch.object(desktop_build, "bootstrap_local_tools"),
            mock.patch.object(desktop_build, "parse_args", return_value=args),
            mock.patch.object(
                desktop_build, "selected_platforms", return_value=["linux"]
            ),
            mock.patch.object(desktop_build, "build_service") as service,
            mock.patch.object(desktop_build, "build_cli") as cli,
            mock.patch.object(desktop_build, "log"),
        ):
            desktop_build.main()

        service.assert_called_once_with("linux", "amd64", True, False, False)
        cli.assert_called_once_with("linux", "amd64")

    def test_windows_wintun_and_bridge_are_staged_for_upload_and_packaging(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            go_module = root / "go_module"
            services = root / "services"
            go_module.mkdir()
            services.mkdir()
            wintun = go_module / "wintun.dll"
            wintun.write_bytes(b"wintun")
            bridge_dir = go_module / "lib" / "windows"
            bridge_dir.mkdir(parents=True)
            bridge = bridge_dir / "dobby_bridge.dll"
            bridge.write_bytes(b"bridge")

            def digest(path: Path) -> str:
                if path.name == "wintun.dll":
                    return desktop_build.WINTUN_AMD64_DLL_SHA256
                return desktop_build.BRIDGE_RELEASES["windows"].member_sha256

            with (
                mock.patch.object(desktop_build, "host_platform", return_value="windows"),
                mock.patch.object(desktop_build, "GO_MODULE_DIR", go_module),
                mock.patch.object(desktop_build, "SERVICES_DIR", services),
                mock.patch.object(desktop_build, "go_arch_from_machine", return_value="amd64"),
                mock.patch.object(
                    desktop_build,
                    "sha256_file",
                    side_effect=digest,
                ),
            ):
                desktop_build.install_wintun(True)
                desktop_build.install_windows_bridge(True)

            self.assertEqual((services / "wintun.dll").read_bytes(), wintun.read_bytes())
            self.assertEqual((go_module / "dobby_bridge.dll").read_bytes(), bridge.read_bytes())
            self.assertEqual((services / "dobby_bridge.dll").read_bytes(), bridge.read_bytes())

    def test_windows_artifact_and_msi_require_complete_runtime_closure(self) -> None:
        workflows = SCRIPT_PATH.parents[1] / "workflows"
        desktop_libraries = (workflows / "desktop_libs_generate.yml").read_text(
            encoding="utf-8",
        )
        installers = (workflows / "installers_build.yml").read_text(encoding="utf-8")
        build_batch = (SCRIPT_PATH.parents[2] / "installer" / "windows" / "build.bat").read_text(
            encoding="utf-8",
        )
        wix = (SCRIPT_PATH.parents[2] / "installer" / "windows" / "AppComponents.wxs").read_text(
            encoding="utf-8",
        )

        for name in ("windows_grpcvpnserver.exe", "dobby_bridge.dll", "wintun.dll"):
            self.assertEqual(desktop_libraries.count(f"go_module/{name}"), 2)
            self.assertIn(name, build_batch)
            self.assertIn(f'"{name}"', installers)
        self.assertIn("WINTUN_AMD64_DLL_SHA256", desktop_build.__dict__)
        self.assertNotIn("curl -#fLo wintun.zip", build_batch)
        self.assertIn("Verify Windows service runtime closure", desktop_libraries)
        self.assertIn("Windows service runtime closure is missing a regular $file", desktop_libraries)
        self.assertIn("checksum mismatch", desktop_libraries)
        self.assertIn("SELECT `FileName` FROM `File`", installers)
        self.assertIn("Windows MSI runtime closure is missing $required", installers)
        self.assertIn('Include="dobbyVPN-windows\\bin\\**.dll"', wix)

    def test_macos_service_links_static_bridge_dependencies(self) -> None:
        build_environments: list[dict[str, str]] = []

        def record_run(*args: object, **kwargs: object) -> None:
            environment = kwargs.get("env")
            if isinstance(environment, dict):
                build_environments.append(environment)

        with (
            mock.patch.object(desktop_build, "ensure_build_dependencies"),
            mock.patch.object(desktop_build, "go_mod_download"),
            mock.patch.object(desktop_build, "run", side_effect=record_run),
            mock.patch.object(desktop_build.shutil, "copyfile"),
            mock.patch.object(desktop_build.Path, "mkdir"),
            mock.patch.object(desktop_build.Path, "chmod"),
            mock.patch.object(desktop_build.Path, "stat", return_value=mock.Mock(st_mode=0o644)),
        ):
            desktop_build.build_service("macos", "arm64", True, False, False)

        self.assertEqual(len(build_environments), 1)
        self.assertIn(
            "-lc++ -framework SystemConfiguration",
            build_environments[0]["CGO_LDFLAGS"],
        )


if __name__ == "__main__":
    unittest.main()
