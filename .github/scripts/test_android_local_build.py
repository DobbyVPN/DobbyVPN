"""Behavioral coverage for the disposable, single-build Android path."""

from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


DRIVER = Path(__file__).with_name("android_build_driver.sh")


def _executable(path: Path, body: str) -> None:
    path.write_text(body, encoding="utf-8")
    path.chmod(0o755)


class AndroidLocalBuildTests(unittest.TestCase):
    def test_local_build_assembles_the_app_once_without_release_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            fake_bin = Path(temporary) / "bin"
            sdk = Path(temporary) / "android-sdk"
            ndk = Path(temporary) / "android-ndk"
            go_root = Path(temporary) / "go-root"
            go_path = Path(temporary) / "go-path"
            output = Path(temporary) / "candidate.apk"
            manifest = Path(temporary) / "manifest.json"
            first_output = Path(temporary) / "first.apk"
            reproducibility = Path(temporary) / "reproducibility.json"
            dependency_manifest = Path(temporary) / "dependency.json"
            gradle_log = Path(temporary) / "gradle-calls.log"

            for relative in (
                "android_module/app",
                "go_module",
                ".github/android",
                ".github/scripts",
                go_path / "bin",
                go_root,
                sdk / "build-tools/36.0.0",
                ndk,
            ):
                (root / relative if isinstance(relative, str) else relative).mkdir(
                    parents=True, exist_ok=True
                )
            (root / "android_module/gradle.properties").write_text(
                "versionName=1.2.3\nversionCode=123\n", encoding="utf-8"
            )
            (root / ".go-version").write_text("1.25.1\n", encoding="utf-8")
            (root / "go_module/go.mod").write_text("module fixture\n", encoding="utf-8")
            (root / "go_module/go.sum").write_text("", encoding="utf-8")
            (root / ".github/android/dependency-spec.json").write_text("{}\n", encoding="utf-8")
            for name in (
                "android_dependency_provenance.py",
                "verify_android_apk_source.py",
                "verify_android_reproducibility.py",
            ):
                (root / ".github/scripts" / name).write_text(
                    "# synthetic test helper\n", encoding="utf-8"
                )
            (sdk / "build-tools/36.0.0/apksigner").write_text(
                "#!/bin/sh\nexit 0\n", encoding="utf-8"
            )
            (sdk / "build-tools/36.0.0/apksigner").chmod(0o755)
            (ndk / "source.properties").write_text(
                "Pkg.Revision = 27.3.13750724\n", encoding="utf-8"
            )

            fake_bin.mkdir()
            _executable(
                fake_bin / "go",
                """#!/bin/sh
set -eu
case "${1:-}" in
  env)
    case "${2:-}" in
      GOVERSION) printf 'go1.25.1\n' ;;
      GOROOT) printf '%s\n' "$GOROOT" ;;
      GOPATH) printf '%s\n' "$GOPATH" ;;
      *) exit 2 ;;
    esac
    ;;
  version)
    printf 'path/tool\n\tgolang.org/x/mobile %s\n' "v0.0.0-20260520154334-0e4426e1883d"
    ;;
  list)
    printf 'v0.0.0-20260520154334-0e4426e1883d\n'
    ;;
  *) exit 2 ;;
esac
""",
            )
            _executable(
                fake_bin / "java",
                """#!/bin/sh
[ "${1:-}" = "-version" ] || exit 2
echo 'openjdk version "17.0.16"' >&2
""",
            )
            _executable(
                fake_bin / "gradle",
                """#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo 'Gradle 8.13'
  exit 0
fi
echo "$*" >> "$GRADLE_CALL_LOG"
case "$*" in
  *:app:assembleReleaseAndroidTest*)
    mkdir -p android_module/app/build/outputs/apk/androidTest/release
    printf 'synthetic companion\n' > android_module/app/build/outputs/apk/androidTest/release/app-release-androidTest.apk
    ;;
  *:app:assembleRelease*)
    mkdir -p android_module/app/build/outputs/apk/release
    printf 'synthetic local app\n' > android_module/app/build/outputs/apk/release/app-release-unsigned.apk
    ;;
  *clean*)
    echo 'unexpected clean' >&2
    exit 9
    ;;
  *) exit 2 ;;
esac
""",
            )
            _executable(
                fake_bin / "python3",
                """#!/bin/sh
case "$*" in
  *android_dependency_provenance.py*--print-mobile-version*)
    printf 'golang.org/x/mobile@v0.0.0-20260520154334-0e4426e1883d\\n'
    ;;
  *) exit 2 ;;
esac
""",
            )
            environment = {
                **os.environ,
                "PATH": str(fake_bin) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                "GRADLE_BIN": str(fake_bin / "gradle"),
                "GO_BIN": str(fake_bin / "go"),
                "JAVA_BIN": str(fake_bin / "java"),
                "GOROOT": str(go_root),
                "GOPATH": str(go_path),
                "ANDROID_SDK_ROOT": str(sdk),
                "ANDROID_NDK_HOME": str(ndk),
                "GRADLE_CALL_LOG": str(gradle_log),
            }
            completed = subprocess.run(
                [
                    str(DRIVER),
                    "--source-root",
                    str(root),
                    "--output",
                    str(output),
                    "--local",
                ],
                cwd=root,
                env=environment,
                capture_output=True,
                text=True,
                check=False,
            )

            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertEqual(output.read_text(encoding="utf-8"), "synthetic local app\n")
            calls = gradle_log.read_text(encoding="utf-8").splitlines()
            app_builds = [call for call in calls if ":app:assembleRelease " in call]
            self.assertEqual(len(app_builds), 1, calls)
            self.assertFalse(any(" clean " in f" {call} " for call in calls), calls)
            self.assertFalse(first_output.exists())
            self.assertFalse(reproducibility.exists())
            self.assertFalse(dependency_manifest.exists())
            self.assertFalse(manifest.exists())
            self.assertIn("-PprojectRepositoryCommit=local", app_builds[0])
            self.assertIn("-PprojectRepositoryCommitLink=", app_builds[0])


if __name__ == "__main__":
    unittest.main()
