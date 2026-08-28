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

    @unittest.skipIf(os.name == "nt", "POSIX process identity assertion")
    def test_process_state_change_is_not_pid_reuse(self) -> None:
        with mock.patch.object(desktop_build, "_proc_identity", return_value=("S", "123")):
            self.assertTrue(desktop_build._pid_is_alive(123, ("R", "123")))
        with mock.patch.object(desktop_build, "_proc_identity", return_value=("S", "456")):
            self.assertFalse(desktop_build._pid_is_alive(123, ("R", "123")))

    def test_windows_process_liveness_uses_read_only_census(self) -> None:
        with (
            mock.patch.object(desktop_build.os, "name", "nt"),
            mock.patch.object(
                desktop_build,
                "windows_process_census",
                return_value=(set(), {123}),
            ) as census,
            mock.patch.object(desktop_build.os, "kill") as kill,
        ):
            self.assertTrue(desktop_build._pid_is_alive(123))
        census.assert_called_once_with(
            123,
            timeout_seconds=desktop_build.PROCESS_TREE_QUERY_TIMEOUT_SECONDS,
        )
        kill.assert_not_called()

    def test_windows_process_liveness_proves_absence_from_census(self) -> None:
        with (
            mock.patch.object(desktop_build.os, "name", "nt"),
            mock.patch.object(
                desktop_build,
                "windows_process_census",
                return_value=(set(), {456}),
            ),
        ):
            self.assertFalse(desktop_build._pid_is_alive(123))

    def test_windows_process_liveness_query_error_fails_closed(self) -> None:
        with (
            mock.patch.object(desktop_build.os, "name", "nt"),
            mock.patch.object(
                desktop_build,
                "windows_process_census",
                side_effect=desktop_build.WindowsProcessCensusError("query failed"),
            ),
            self.assertRaisesRegex(desktop_build.ProcessTreeProofError, "query failed"),
        ):
            desktop_build._pid_is_alive(123)

    @unittest.skipIf(os.name == "nt", "POSIX ps fallback assertion")
    def test_ps_fallback_timeout_retains_partial_streams_and_marks_incomplete(self) -> None:
        timeout = subprocess.TimeoutExpired(
            ["ps"],
            10,
            output=b"ps stdout\n",
            stderr=b"ps stderr\n",
        )
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with (
                mock.patch.object(desktop_build, "ROOT_DIR", root),
                mock.patch.object(desktop_build.subprocess, "run", side_effect=timeout),
                self.assertRaisesRegex(
                    desktop_build.ProcessTreeProofError,
                    r"stdout=ps stdout stderr=ps stderr evidence_incomplete=1",
                ),
            ):
                desktop_build._ps_descendants(123)
            logs = list((root / "runtime" / "desktop-build-diagnostics").glob("*.log"))
            self.assertEqual(len(logs), 1)
            self.assertIn("ps stdout", logs[0].read_text(encoding="utf-8"))

    def test_windows_tree_proof_uses_final_census_without_signalling(self) -> None:
        sample_count = 0

        def snapshot(_root_pid: int) -> tuple[set[int], str, set[int]]:
            nonlocal sample_count
            sample_count += 1
            if sample_count == 1:
                return {200}, "powershell-cim", {100, 200}
            return set(), "powershell-cim", {999}

        with (
            mock.patch.object(desktop_build.os, "name", "nt"),
            mock.patch.object(
                desktop_build,
                "_process_tree_snapshot",
                side_effect=snapshot,
            ),
            mock.patch.object(
                desktop_build,
                "_pid_is_alive",
                side_effect=AssertionError("Windows proof must use the census"),
            ),
            mock.patch.object(
                desktop_build,
                "PROCESS_TREE_POLL_INTERVAL_SECONDS",
                60,
            ),
        ):
            tracker = desktop_build.ProcessTreeTracker(100)
            tracker.start()
            proof = tracker.prove_gone(100)
        self.assertEqual(proof, "tree=gone source=powershell-cim observed_pids=2")

    def test_windows_tree_proof_rejects_census_survivor(self) -> None:
        sample_count = 0

        def snapshot(_root_pid: int) -> tuple[set[int], str, set[int]]:
            nonlocal sample_count
            sample_count += 1
            if sample_count == 1:
                return {200}, "powershell-cim", {100, 200}
            return set(), "powershell-cim", {200}

        with (
            mock.patch.object(desktop_build.os, "name", "nt"),
            mock.patch.object(
                desktop_build,
                "_process_tree_snapshot",
                side_effect=snapshot,
            ),
            mock.patch.object(
                desktop_build,
                "PROCESS_TREE_POLL_INTERVAL_SECONDS",
                60,
            ),
            self.assertRaisesRegex(
                desktop_build.ProcessTreeProofError,
                r"descendant survivors=\[200\]",
            ),
        ):
            tracker = desktop_build.ProcessTreeTracker(100)
            tracker.start()
            tracker.prove_gone(100)

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
        self.assertEqual(
            service["ProgramArguments"][0],
            "/Applications/Dobby VPN.app/Contents/Resources/macos_grpcvpnserver",
        )
        self.assertEqual(
            service["WorkingDirectory"],
            "/Applications/Dobby VPN.app/Contents/Resources/",
        )

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

    def test_curl_download_has_bounded_transfer_and_retry_time(self) -> None:
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
        self.assertEqual(command[command.index("--retry-max-time") + 1], "1200")

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

    def test_conveyor_config_uses_host_gradle_wrapper_and_emits_only_hocon(self) -> None:
        completed = mock.Mock(returncode=0, stdout="app.display-name = DobbyVPN\n")
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
        self.assertEqual(output.getvalue(), completed.stdout)
        self.assertEqual(diagnostics.getvalue(), "jdk-ready\n")
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
            mock.patch.object(desktop_build, "attach_process_tree_tracker") as attach_tracker,
        ):
            started, _ = desktop_build.start_service("linux", 50151, socket_path)

        self.assertIs(started, process)
        attach_tracker.assert_called_once_with(process)
        wait_for_socket.assert_called_once_with(socket_path)
        wait_for_port.assert_not_called()
        command = popen.call_args.args[0]
        self.assertIn(f"DOBBYVPN_CONTROL_SOCKET={socket_path}", command)
        self.assertEqual(
            popen.call_args.kwargs["env"]["DOBBYVPN_CONTROL_SOCKET"],
            str(socket_path),
        )

    def test_service_logs_are_unique_owner_only_and_never_fixed_paths(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(desktop_build, "ROOT_DIR", root):
                first = desktop_build.open_service_log("combined")
                second = desktop_build.open_service_log("combined")
                try:
                    self.assertNotEqual(first.name, second.name)
                    self.assertEqual(Path(first.name).stat().st_mode & 0o777, 0o600)
                    self.assertEqual(Path(second.name).stat().st_mode & 0o777, 0o600)
                    self.assertEqual(
                        (root / "runtime" / "desktop-build-diagnostics").stat().st_mode & 0o777,
                        0o700,
                    )
                finally:
                    first.close()
                    second.close()
            source = SCRIPT_PATH.read_text(encoding="utf-8")
            for fixed_name in ("grpcvpnserver.log", "grpcvpnserver.out", "grpcvpnserver.err"):
                self.assertNotIn(f'open(ROOT_DIR / "{fixed_name}", "w"', source)

    def test_service_log_reporting_keeps_complete_output_after_close(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(desktop_build, "ROOT_DIR", root):
                handle = desktop_build.open_service_log("combined")
                handle.write("first diagnostic line\nsecond diagnostic line\n")
                handle.close()
                output = io.StringIO()
                with mock.patch.object(desktop_build.sys, "stdout", output):
                    desktop_build.print_service_logs([handle])
        self.assertIn("first diagnostic line\nsecond diagnostic line\n", output.getvalue())

    def test_public_service_log_reporting_retains_raw_bytes_without_public_echo(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(desktop_build, "ROOT_DIR", root), mock.patch.dict(
                os.environ,
                {"GITHUB_ACTIONS": "true", "RUNNER_TEMP": temporary},
                clear=False,
            ):
                handle = desktop_build.open_service_log("combined")
                handle.write("private service endpoint /tmp/private.sock\n")
                handle.close()
                output = io.StringIO()
                diagnostics = io.StringIO()
                with (
                    mock.patch.object(desktop_build.sys, "stdout", output),
                    mock.patch.object(desktop_build.sys, "stderr", diagnostics),
                ):
                    desktop_build.print_service_logs([handle])
            self.assertNotIn("private service endpoint", output.getvalue())
            self.assertNotIn("private service endpoint", diagnostics.getvalue())
            retained = list((Path(temporary) / "dobbyvpn-public-diagnostics").glob("*.raw.log"))
            self.assertEqual(len(retained), 1)
            self.assertIn("private service endpoint", retained[0].read_text(encoding="utf-8"))
            self.assertEqual(retained[0].stat().st_mode & 0o777, 0o600)

    def test_public_process_diagnostic_retains_raw_bytes_without_public_echo(self) -> None:
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
            self.assertNotIn("private stderr", diagnostics.getvalue())
            self.assertIn("dobbyvpn_diagnostic kind=desktop-build", diagnostics.getvalue())
            retained = list((Path(temporary) / "dobbyvpn-public-diagnostics").glob("*.raw.log"))
            self.assertEqual(len(retained), 1)
            self.assertIn(b"private stderr\xff\n", retained[0].read_bytes())

    def test_probe_diagnostics_are_unique_and_non_overwriting(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(desktop_build, "ROOT_DIR", root):
                first = desktop_build.retain_process_diagnostics(
                    "probe", "first stdout\n", "first stderr\n", "exit-1",
                )
                second = desktop_build.retain_process_diagnostics(
                    "probe", "second stdout\n", "second stderr\n", "exit-2",
                )
            self.assertNotEqual(first, second)
            self.assertIn("first stderr\n", first.read_text(encoding="utf-8"))
            self.assertIn("second stderr\n", second.read_text(encoding="utf-8"))

    def test_probe_diagnostics_preserve_raw_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            with mock.patch.object(desktop_build, "ROOT_DIR", Path(temporary)):
                retained = desktop_build.retain_process_diagnostics(
                    "probe", b"stdout-\xff\n", b"stderr-\xfe\n", "exit-1",
                )
            retained_bytes = retained.read_bytes()
        self.assertIn(b"stdout-\xff\n", retained_bytes)
        self.assertIn(b"stderr-\xfe\n", retained_bytes)

    def test_bounded_probe_final_drain_timeout_retains_each_partial_stream_once(self) -> None:
        initial = subprocess.TimeoutExpired(
            ["probe"],
            1,
            output=b"initial stdout\n",
            stderr=b"initial stderr\n",
        )
        final = subprocess.TimeoutExpired(
            ["probe"],
            5,
            output=b"final stdout\n",
            stderr=b"final stderr\n",
        )
        process = mock.Mock(pid=123)
        process.communicate.side_effect = [initial, final]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with (
                mock.patch.object(desktop_build, "ROOT_DIR", root),
                mock.patch.object(desktop_build.subprocess, "Popen", return_value=process),
                mock.patch.object(desktop_build, "attach_process_tree_tracker"),
                mock.patch.object(
                    desktop_build,
                    "terminate_process_group",
                    return_value="tree=gone source=test observed_pids=1",
                ),
                mock.patch.object(desktop_build, "PROCESS_CLEANUP_GRACE_SECONDS", 0.1),
            ):
                with self.assertRaises(subprocess.TimeoutExpired) as raised:
                    desktop_build.run_bounded_capture(
                        ["probe"], cwd=root, timeout_seconds=1,
                    )
            self.assertEqual(raised.exception.output.count("stdout"), 2)
            self.assertEqual(raised.exception.stderr.count("stderr"), 2)
            logs = list((root / "runtime" / "desktop-build-diagnostics").glob("grpcvpnserver-probe-*.log"))
            self.assertEqual(len(logs), 1)
            log_text = logs[0].read_text(encoding="utf-8")
            self.assertIn("evidence_incomplete=1", log_text)
            self.assertEqual(log_text.count("initial stdout"), 1)
            self.assertEqual(log_text.count("final stdout"), 1)
            self.assertEqual(log_text.count("initial stderr"), 1)
            self.assertEqual(log_text.count("final stderr"), 1)

    def test_bounded_probe_oserror_retains_all_partial_streams_once(self) -> None:
        error = OSError("communicate pipe failed")
        error.stdout = b"partial stdout\n"  # type: ignore[attr-defined]
        error.output = b"partial stdout\n"  # type: ignore[attr-defined]
        error.stderr = b"partial stderr\n"  # type: ignore[attr-defined]
        process = mock.Mock(pid=123)
        process.communicate.side_effect = [error, (b"drained stdout\n", b"drained stderr\n")]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with (
                mock.patch.object(desktop_build, "ROOT_DIR", root),
                mock.patch.object(desktop_build.subprocess, "Popen", return_value=process),
                mock.patch.object(desktop_build, "attach_process_tree_tracker"),
                mock.patch.object(
                    desktop_build,
                    "terminate_process_group",
                    return_value="tree=gone source=test observed_pids=1",
                ),
            ):
                with self.assertRaises(OSError) as raised:
                    desktop_build.run_bounded_capture(["probe"], cwd=root, timeout_seconds=1)
            self.assertEqual(raised.exception.stdout.count("stdout"), 2)  # type: ignore[attr-defined]
            self.assertEqual(raised.exception.stderr.count("stderr"), 2)  # type: ignore[attr-defined]
            logs = list((root / "runtime" / "desktop-build-diagnostics").glob("grpcvpnserver-probe-*.log"))
            self.assertEqual(len(logs), 1)
            log_text = logs[0].read_text(encoding="utf-8")
            self.assertNotIn("evidence_incomplete=1", log_text)
            self.assertEqual(log_text.count("partial stdout"), 1)
            self.assertEqual(log_text.count("drained stdout"), 1)

    def test_windows_taskkill_exception_retains_partial_streams(self) -> None:
        error = subprocess.TimeoutExpired(
            ["taskkill"],
            0.1,
            output=b"taskkill stdout\n",
            stderr=b"taskkill stderr\n",
        )
        tracker = mock.Mock()
        tracker.prove_gone.side_effect = [
            desktop_build.ProcessTreeProofError("descendant survivors=[456]"),
            "tree=gone source=test observed_pids=2",
        ]
        tracker.observed_pids.return_value = (123, 456)
        process = mock.Mock(pid=123)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with (
                mock.patch.object(desktop_build, "ROOT_DIR", root),
                mock.patch.object(desktop_build.os, "name", "nt"),
                mock.patch.object(desktop_build.signal, "CTRL_BREAK_EVENT", 1, create=True),
                mock.patch.object(desktop_build, "process_tree_tracker", return_value=tracker),
                mock.patch.object(desktop_build.subprocess, "run", side_effect=error),
                mock.patch.object(desktop_build, "retain_process_diagnostics") as retain,
            ):
                proof = desktop_build.terminate_process_group(process, grace_seconds=0.1)
            self.assertEqual(proof, "tree=gone source=test observed_pids=2")
            self.assertEqual(retain.call_count, 2)
            for call in retain.call_args_list:
                self.assertIn(b"taskkill stdout\n", call.args)
                self.assertIn(b"taskkill stderr\n", call.args)
                self.assertIn("evidence_incomplete=1", call.args[3])

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
                    if not desktop_build._pid_is_alive(child_pid):
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
    def test_bounded_probe_timeout_retains_streams_and_kills_descendants(self) -> None:
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
            logs = list((root / "runtime" / "desktop-build-diagnostics").glob("grpcvpnserver-probe-*.log"))
            self.assertEqual(len(logs), 1)
            log_text = logs[0].read_text(encoding="utf-8")
            self.assertIn("childpid=", log_text)
            self.assertIn("probe stderr", log_text)
            for _ in range(30):
                if not desktop_build._pid_is_alive(child_pid):
                    break
                time.sleep(0.05)
            else:
                self.fail("timed-out probe descendant survived cleanup")

    @unittest.skipIf(os.name == "nt", "POSIX detached-process assertion")
    def test_zero_exit_leader_detached_resistant_descendant_is_proven_gone(self) -> None:
        child_code = (
            "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "time.sleep(60)"
        )
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            child_stdout = root / "child.stdout.raw.log"
            child_stderr = root / "child.stderr.raw.log"
            parent_code = (
                "import os,subprocess,sys,time; "
                f"child_stdout=os.fdopen(os.open({str(child_stdout)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child_stderr=os.fdopen(os.open({str(child_stderr)!r}, os.O_WRONLY|os.O_CREAT|os.O_APPEND, 0o600), 'ab', buffering=0); "
                f"child=subprocess.Popen([sys.executable,'-c',{child_code!r}], "
                "start_new_session=True, stdout=child_stdout, stderr=child_stderr); "
                "childstat=open('/proc/%s/stat' % child.pid).read(); "
                "print('childpid='+str(child.pid), flush=True); "
                "print('childstart='+childstat[childstat.rfind(')')+2:].split()[19], flush=True); time.sleep(1.5)"
            )
            with mock.patch.object(desktop_build, "ROOT_DIR", root):
                result = desktop_build.run_bounded_capture(
                    [sys.executable, "-c", parent_code], cwd=root, timeout_seconds=2,
                )
            child_output = result.stdout
            child_pid = int(child_output.split("childpid=", 1)[1].splitlines()[0])
            child_start = child_output.split("childstart=", 1)[1].splitlines()[0]
            logs = list((root / "runtime" / "desktop-build-diagnostics").glob("grpcvpnserver-probe-*.log"))
            self.assertEqual(len(logs), 1)
            self.assertIn(b"tree=gone", logs[0].read_bytes())
            for _ in range(30):
                identity = desktop_build._proc_identity(child_pid)
                if identity is None or identity[1] != child_start or identity[0] == "Z":
                    break
                time.sleep(0.05)
            else:
                self.fail("zero-exit detached descendant survived process-tree cleanup")
            self.assertEqual(child_stdout.stat().st_mode & 0o777, 0o600)
            self.assertEqual(child_stderr.stat().st_mode & 0o777, 0o600)

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
                mock.call(["sudo", "unlink", str(socket_path)], check=False),
                mock.call(["sudo", "rmdir", str(socket_path.parent)], check=False),
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
            mock.patch.object(desktop_build, "run", side_effect=lambda *args, **kwargs: calls.append("build")),
            mock.patch.object(desktop_build.shutil, "copyfile"),
            mock.patch.object(desktop_build.Path, "mkdir"),
        ):
            desktop_build.build_service("windows", "amd64", True, False, False)

        self.assertEqual(calls, ["wintun:True", "bridge:True", "build"])

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
