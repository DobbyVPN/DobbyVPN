from __future__ import annotations

from copy import deepcopy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml

from android_dependency_provenance import dependency_pins
from fdroid_candidate_metadata import (
    CandidateMetadataError,
    finalize_metadata,
    prepare_metadata,
)


VERSION_NAME = "1.5.2"
VERSION_CODE = 1005002
VERSION_URL = "https://example.invalid/version.txt"
HISTORICAL_SHA = "1" * 40
GO_SOURCE_SHA = "2" * 40


def _write_yaml(path: Path, document: dict) -> None:
    path.write_text(yaml.safe_dump(document, sort_keys=False), encoding="utf-8")


def _base_metadata() -> dict:
    return {
        "License": "Apache-2.0",
        "Name": "DobbyVPN",
        "RepoType": "git",
        "Repo": "https://github.com/DobbyVPN/DobbyVPN.git",
        "Builds": [
            {
                "versionName": "1.4.0",
                "versionCode": 1004000,
                "commit": HISTORICAL_SHA,
                "subdir": "ui/android",
                "gradle": ["yes"],
                "srclibs": ["go@go1.24.0"],
                "preassemble": ["obsoleteTask"],
                "rm": ["old/path"],
                "postbuild": ["zipalign -f 4 $$OUT$$ $$OUT$$.aligned"],
            }
        ],
        "Binaries": "https://example.invalid/dobbyvpn-%c.apk",
        "AllowedAPKSigningKeys": "A" * 64,
        "UpdateCheckMode": "HTTP",
        "AutoUpdateMode": "Version v%v",
        "UpdateCheckName": "com.dobby.vpn",
        "UpdateCheckData": (
            "https://upstream.invalid/version.txt|versionCode=(\\d+)"
            "|gradle.properties|versionName=(\\d+\\.\\d+\\.\\d+)"
        ),
        "CurrentVersion": "1.4.0",
        "CurrentVersionCode": 1004000,
    }


def _make_source_root(root: Path) -> tuple[Path, str]:
    root.mkdir(parents=True)
    (root / ".go-version").write_text("1.26.8\n", encoding="utf-8")
    (root / ".github/scripts/android").mkdir(parents=True)
    (root / ".github/scripts/android/dependency-spec.json").write_text(
        json.dumps(
            {
                "schema": 1,
                "kind": "dobbyvpn.android.dependency-spec",
                "go": {"source_commit": GO_SOURCE_SHA},
                "android": {"build_tools": "35.0.0"},
            }
        ),
        encoding="utf-8",
    )
    (root / "core").mkdir()
    (root / "core/go.mod").write_text(
        "module github.com/DobbyVPN/DobbyVPN/core\n\n"
        "go 1.26.8\n\n"
        "require (\n"
        "    golang.org/x/mobile v0.0.0-20260101000000-abcdefabcdef\n"
        ")\n",
        encoding="utf-8",
    )
    wrapper = root / "ui/android/gradle/wrapper"
    wrapper.mkdir(parents=True)
    (wrapper / "gradle-wrapper.properties").write_text(
        "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.14-bin.zip\n"
        f"distributionSha256Sum={'3' * 64}\n",
        encoding="utf-8",
    )
    app = root / "ui/android/app"
    app.mkdir(parents=True)
    (app / "build.gradle.kts").write_text(
        "JavaVersion.VERSION_17\n"
        'jvmTarget = JvmTarget.fromTarget("17")\n'
        'val pinnedAndroidNdkVersion = "28.1.13356709"\n'
        "compileSdk = 35\n",
        encoding="utf-8",
    )
    (root / "ui/android").mkdir(exist_ok=True)
    (root / "ui/android/gradle.properties").write_text(
        "versionCode=1005002\nversionName=1.5.2\npackageName=com.dobby.vpn\n",
        encoding="utf-8",
    )

    subprocess.run(["git", "init", "-q", str(root)], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.name", "Metadata Test"], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.email", "metadata@example.invalid"], check=True)
    subprocess.run(["git", "-C", str(root), "add", "."], check=True)
    subprocess.run(["git", "-C", str(root), "commit", "-qm", "Fixture source"], check=True)
    sha = subprocess.run(
        ["git", "-C", str(root), "rev-parse", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    return root, sha


class FdroidCandidateMetadataTests(unittest.TestCase):
    def setUp(self) -> None:
        self._reset_fixture()

    def _reset_fixture(self) -> None:
        if hasattr(self, "temporary"):
            self.temporary.cleanup()
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.metadata_path = self.root / "metadata/com.dobby.vpn.yml"
        self.baseline_path = self.root / "live-baseline.yml"
        self.mirror_path = self.root / "candidate-mirror.git"
        self.mirror_path.mkdir()
        self.source_root, self.source_sha = _make_source_root(self.root / "source")
        self.metadata_path.parent.mkdir(parents=True)
        self.live_yaml = yaml.safe_dump(_base_metadata(), sort_keys=False)
        self.metadata_path.write_text(self.live_yaml, encoding="utf-8")

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _prepare(self, **overrides) -> None:
        arguments = {
            "metadata_path": self.metadata_path,
            "baseline_path": self.baseline_path,
            "mirror_repo": self.mirror_path,
            "version_url": VERSION_URL,
            "version_name": VERSION_NAME,
            "version_code": VERSION_CODE,
        }
        arguments.update(overrides)
        prepare_metadata(**arguments)

    def _simulate_checkupdates(self, **overrides) -> None:
        document = yaml.safe_load(self.metadata_path.read_text(encoding="utf-8"))
        values = {
            "versionName": VERSION_NAME,
            "versionCode": VERSION_CODE,
            "commit": self.source_sha,
        }
        values.update(overrides)
        new_build = deepcopy(document["Builds"][-1])
        new_build.update(values)
        document["Builds"].append(new_build)
        document["CurrentVersion"] = values["versionName"]
        document["CurrentVersionCode"] = values["versionCode"]
        _write_yaml(self.metadata_path, document)

    def test_prepares_local_update_source_then_patches_only_the_appended_build(self) -> None:
        self._prepare()
        self.assertEqual(self.baseline_path.read_text(encoding="utf-8"), self.live_yaml)
        prepared = yaml.safe_load(self.metadata_path.read_text(encoding="utf-8"))
        self.assertEqual(prepared["Repo"], self.mirror_path.resolve().as_uri())
        self.assertEqual(prepared["UpdateCheckMode"], "HTTP")
        self.assertEqual(prepared["AutoUpdateMode"], "Version v%v")
        self.assertEqual(
            prepared["UpdateCheckData"],
            "https://example.invalid/version.txt|versionCode=(\\d+)|.|versionName=(\\d+\\.\\d+\\.\\d+)",
        )

        self._simulate_checkupdates()
        observed = yaml.safe_load(self.metadata_path.read_text(encoding="utf-8"))
        observed["AutoName"] = "DobbyVPN"
        _write_yaml(self.metadata_path, observed)
        result = finalize_metadata(
            self.metadata_path,
            self.baseline_path,
            self.source_root,
            self.source_sha,
            VERSION_NAME,
            VERSION_CODE,
        )

        final = yaml.safe_load(self.metadata_path.read_text(encoding="utf-8"))
        old_build, candidate = final["Builds"]
        baseline = yaml.safe_load(self.baseline_path.read_text(encoding="utf-8"))
        pins = dependency_pins(self.source_root)
        self.assertEqual(old_build, baseline["Builds"][0])
        self.assertEqual(old_build["preassemble"], ["obsoleteTask"])
        self.assertEqual(old_build["rm"], ["old/path"])
        self.assertEqual(candidate["versionName"], VERSION_NAME)
        self.assertEqual(candidate["versionCode"], VERSION_CODE)
        self.assertEqual(candidate["commit"], self.source_sha)
        self.assertEqual(candidate["subdir"], "ui/android")
        self.assertEqual(candidate["gradle"], ["yes"])
        self.assertEqual(
            candidate["srclibs"],
            [f"go@go{pins['go_version']}"],
        )
        self.assertEqual(candidate["target"], f"android-{pins['android_compile_sdk']}")
        self.assertEqual(candidate["ndk"], str(pins["android_ndk"]))
        self.assertEqual(candidate["output"], "app/build/outputs/apk/release/app-release-unsigned.apk")
        self.assertNotIn("preassemble", candidate)
        self.assertNotIn("rm", candidate)
        self.assertNotIn("postbuild", candidate)
        recipe_script = "\n".join(candidate["build"])
        self.assertIn(f"test \"$(git -C \"$go_root\" rev-parse --verify HEAD^{{commit}})\" = \"{pins['go_source_commit']}\"", recipe_script)
        self.assertIn("./make.bash", recipe_script)
        self.assertIn('export GOROOT="$$go$$"', recipe_script)
        self.assertIn("export GOPATH=/home/vagrant/go", recipe_script)
        self.assertIn('export GOROOT_FINAL="$GOROOT"', recipe_script)
        self.assertIn("export GOTOOLCHAIN=local", recipe_script)
        self.assertIn('export GOFLAGS="-trimpath -buildvcs=false"', recipe_script)
        self.assertIn("export SOURCE_DATE_EPOCH=0", recipe_script)
        self.assertIn(f'"platforms;android-{pins["android_compile_sdk"]}"', recipe_script)
        self.assertIn(f'"build-tools;{pins["android_build_tools"]}"', recipe_script)
        self.assertEqual(
            candidate["gradleprops"],
            [
                "android.injected.version.name=1.5.2",
                "android.injected.version.code=1005002",
                "packageName=com.dobby.vpn",
                f"projectRepositoryCommit={self.source_sha}",
                f"dobbyGoBinary={self.source_root.resolve().parent / 'srclib/go/bin/go'}",
            ],
        )
        self.assertEqual(final["AllowedAPKSigningKeys"], [baseline["AllowedAPKSigningKeys"]])
        self.assertNotIn("Binaries", final)
        self.assertEqual(final["License"], "BUSL-1.1")
        self.assertEqual(result["phase"], "finalize")
        self.assertEqual(result["go_source_commit"], pins["go_source_commit"])

    def test_rejects_non_new_candidate_version(self) -> None:
        with self.assertRaisesRegex(CandidateMetadataError, "must be newer"):
            self._prepare(version_code=1004000)

    def test_rejects_disabled_update_modes_ignored_name_lookup_and_bad_regexes(self) -> None:
        base = _base_metadata()
        cases = [
            ({"UpdateCheckMode": "Static"}, "UpdateCheckMode must be HTTP"),
            ({"AutoUpdateMode": "None"}, "AutoUpdateMode must be"),
            ({"UpdateCheckName": "Ignore"}, "disables package-name lookup"),
            ({"UpdateCheckData": "https://example.invalid/v|(|.|(.+)"}, "invalid version-code regex"),
            ({"UpdateCheckData": "https://example.invalid/v|versionCode=(\\d+)|."}, "exactly four"),
        ]
        for changes, message in cases:
            with self.subTest(changes=changes):
                document = deepcopy(base)
                document.update(changes)
                _write_yaml(self.metadata_path, document)
                with self.assertRaisesRegex(CandidateMetadataError, message):
                    self._prepare()
                self.metadata_path.write_text(self.live_yaml, encoding="utf-8")

    def test_rejects_malformed_metadata_schema(self) -> None:
        document = _base_metadata()
        document["Builds"] = {"versionName": "1.4.0", "versionCode": 1004000}
        _write_yaml(self.metadata_path, document)
        with self.assertRaisesRegex(CandidateMetadataError, "Builds must be a non-empty list"):
            self._prepare()

    def test_rejects_wrong_appended_identity_or_source_checkout(self) -> None:
        for overrides, wrong_source_sha, message in (
            ({"versionName": "1.5.3"}, False, "versionName does not match"),
            ({"commit": HISTORICAL_SHA}, False, "Build commit does not match"),
            ({}, True, "source checkout SHA"),
        ):
            with self.subTest(overrides=overrides, wrong_source_sha=wrong_source_sha):
                self._reset_fixture()
                self._prepare()
                self._simulate_checkupdates(**overrides)
                before = self.metadata_path.read_bytes()
                expected_source_sha = HISTORICAL_SHA if wrong_source_sha else self.source_sha
                with self.assertRaisesRegex(CandidateMetadataError, message):
                    finalize_metadata(
                        self.metadata_path,
                        self.baseline_path,
                        self.source_root,
                        expected_source_sha,
                        VERSION_NAME,
                        VERSION_CODE,
                    )
                self.assertEqual(self.metadata_path.read_bytes(), before)

    def test_rejects_a_changed_historical_build(self) -> None:
        self._prepare()
        self._simulate_checkupdates()
        document = yaml.safe_load(self.metadata_path.read_text(encoding="utf-8"))
        document["Builds"][0]["commit"] = "4" * 40
        _write_yaml(self.metadata_path, document)
        with self.assertRaisesRegex(CandidateMetadataError, "historical Builds changed"):
            finalize_metadata(
                self.metadata_path,
                self.baseline_path,
                self.source_root,
                self.source_sha,
                VERSION_NAME,
                VERSION_CODE,
            )

    def test_rejects_an_unexpected_checkupdates_top_level_mutation(self) -> None:
        self._prepare()
        self._simulate_checkupdates()
        document = yaml.safe_load(self.metadata_path.read_text(encoding="utf-8"))
        document["RepoType"] = "git-svn"
        _write_yaml(self.metadata_path, document)
        with self.assertRaisesRegex(CandidateMetadataError, "RepoType changed"):
            finalize_metadata(
                self.metadata_path,
                self.baseline_path,
                self.source_root,
                self.source_sha,
                VERSION_NAME,
                VERSION_CODE,
            )


if __name__ == "__main__":
    unittest.main()
