from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("desktop_build.py")
SPEC = importlib.util.spec_from_file_location("desktop_build", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT_PATH}")
desktop_build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(desktop_build)


class DesktopBuildTests(unittest.TestCase):
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
            mock.patch.object(desktop_build, "prepare_cloak_internal"),
            mock.patch.object(desktop_build, "go_mod_download"),
            mock.patch.object(desktop_build, "run", side_effect=lambda *args, **kwargs: calls.append("build")),
            mock.patch.object(desktop_build.shutil, "copyfile"),
            mock.patch.object(desktop_build.Path, "mkdir"),
        ):
            desktop_build.build_service("windows", "amd64", True, False, False)

        self.assertEqual(calls, ["wintun:True", "mingw-runtime", "build"])

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
