from __future__ import annotations

import importlib.util
from pathlib import Path
import stat
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("desktop_build.py")
SPEC = importlib.util.spec_from_file_location("desktop_build", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT_PATH}")
desktop_build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(desktop_build)


class DesktopBuildTests(unittest.TestCase):
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

        with (
            mock.patch.object(desktop_build, "service_target_path", return_value=Path("/tmp/service")),
            mock.patch.object(desktop_build.Path, "exists", return_value=True),
            mock.patch.object(desktop_build, "sudo_prefix", return_value=["sudo"]),
            mock.patch.object(desktop_build, "wait_for_socket", return_value=True) as wait_for_socket,
            mock.patch.object(desktop_build, "wait_for_port") as wait_for_port,
            mock.patch.object(desktop_build.subprocess, "Popen", return_value=process) as popen,
            mock.patch("builtins.open", mock.mock_open()),
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

    def test_windows_service_stages_runtime_dlls_before_build(self) -> None:
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
                "stage_windows_runtime_dlls",
                side_effect=lambda: calls.append("mingw-runtime"),
            ),
            mock.patch.object(
                desktop_build,
                "install_windows_bridge",
                side_effect=lambda skip_deps: calls.append(f"bridge:{skip_deps}"),
            ),
            mock.patch.object(desktop_build, "prepare_cloak_internal"),
            mock.patch.object(desktop_build, "go_mod_download"),
            mock.patch.object(desktop_build, "run", side_effect=lambda *args, **kwargs: calls.append("build")),
            mock.patch.object(desktop_build.shutil, "copyfile"),
            mock.patch.object(desktop_build.Path, "mkdir"),
        ):
            desktop_build.build_service("windows", "amd64", True, False, False)

        self.assertEqual(calls, ["wintun:True", "mingw-runtime", "bridge:True", "build"])

    def test_macos_service_links_static_bridge_dependencies(self) -> None:
        build_environments: list[dict[str, str]] = []

        def record_run(*args: object, **kwargs: object) -> None:
            environment = kwargs.get("env")
            if isinstance(environment, dict):
                build_environments.append(environment)

        with (
            mock.patch.object(desktop_build, "ensure_build_dependencies"),
            mock.patch.object(desktop_build, "prepare_cloak_internal"),
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
