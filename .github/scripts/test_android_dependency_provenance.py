from __future__ import annotations

import hashlib
import importlib.util
import json
from pathlib import Path
import zipfile

import pytest


SPEC = importlib.util.spec_from_file_location(
    "android_dependency_provenance",
    Path(__file__).with_name("android_dependency_provenance.py"),
)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)

COMMIT = "a" * 40
TREE = "b" * 40


def _spec_document() -> dict[str, object]:
    return {
        "schema": 1,
        "kind": MODULE.KIND,
        "repositories": MODULE.REPOSITORIES,
        "gradle": {
            "distribution_url": MODULE.GRADLE_URL,
            "distribution_sha256": MODULE.GRADLE_SHA256,
        },
        "java": {"major": MODULE.JAVA_MAJOR},
        "android": {"build_tools": MODULE.ANDROID_BUILD_TOOLS, "ndk": MODULE.ANDROID_NDK},
        "go": {"version": MODULE.GO_VERSION, "source_commit": MODULE.GO_SOURCE_COMMIT},
        "go_mobile": {"module": MODULE.MOBILE_MODULE, "version": MODULE.MOBILE_VERSION},
    }


def _source(root: Path, *, wrapper_checksum: bool = True) -> Path:
    for relative in MODULE.DECLARED_INPUTS:
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        if relative.endswith("gradle-wrapper.properties"):
            value = "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.13-bin.zip\n"
            if wrapper_checksum:
                value += f"distributionSha256Sum={MODULE.GRADLE_SHA256}\n"
            path.write_text(value, encoding="utf-8")
        elif relative == ".go-version":
            path.write_text(f"{MODULE.GO_VERSION}\n", encoding="utf-8")
        else:
            path.write_bytes(relative.encode())
    spec = root / ".github/android/dependency-spec.json"
    spec.parent.mkdir(parents=True, exist_ok=True)
    spec.write_text(json.dumps(_spec_document(), sort_keys=True) + "\n", encoding="utf-8")
    return spec


def _gradle_distribution(root: Path) -> tuple[Path, Path]:
    archive = root / "gradle-8.13-bin.zip"
    extracted = root / "gradle-8.13"
    files = {
        "bin/gradle": "#!/bin/sh\necho Gradle 8.13\n",
        "lib/gradle-launcher.jar": "synthetic Gradle 8.13 payload\n",
    }
    for relative, content in files.items():
        path = extracted / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
    (extracted / "bin/gradle").chmod(0o755)
    archive.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(archive, "w") as zipped:
        zipped.writestr("gradle-8.13/", b"")
        for relative, content in files.items():
            zipped.writestr(f"gradle-8.13/{relative}", content)
    return archive, extracted


def test_manifest_binds_the_source_inputs_and_pins(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    first = MODULE.create_manifest(tmp_path, COMMIT, TREE, spec, java_version="17.0.16")
    second = MODULE.create_manifest(tmp_path, COMMIT, TREE, spec, java_version="17.0.16")
    assert first == second
    assert first["dependency_provenance"] == "tracked_dependency_spec"
    assert first["resolution"]["offline_verified"] is False
    assert first["toolchain"]["go_source_commit"] == MODULE.GO_SOURCE_COMMIT
    assert first["inputs"][-1]["path"] == ".github/android/dependency-spec.json"
    assert first["go_modules"][1:] == [dict(module) for module in MODULE.GO_UI_MODULES]


def test_go_ui_replacement_printout_is_exact_and_reusable(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    spec = _source(tmp_path)
    assert MODULE.main(["--spec", str(spec), "--print-go-ui-replacements"]) == 0
    lines = capsys.readouterr().out.splitlines()
    assert lines == [
        "\t".join(
            str(module[key])
            for key in ("module", "version", "replacement_module", "replacement_version", "revision")
        )
        for module in MODULE.GO_UI_MODULES
    ]


def test_manifest_requires_the_current_wrapper_checksum(tmp_path: Path) -> None:
    spec = _source(tmp_path, wrapper_checksum=False)
    with pytest.raises(ValueError, match="wrapper must contain"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, spec)


def test_manifest_rejects_changed_declared_input(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    manifest = MODULE.create_manifest(tmp_path, COMMIT, TREE, spec)
    output = tmp_path / "manifest.json"
    output.write_text(json.dumps(manifest) + "\n", encoding="utf-8")
    (tmp_path / "android_module/settings.gradle.kts").write_bytes(b"changed")
    with pytest.raises(ValueError, match="declared input hashes changed"):
        MODULE.verify_manifest(tmp_path, COMMIT, TREE, spec, output)


def test_archive_proof_matches_the_verified_distribution(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    archive, root = _gradle_distribution(tmp_path / "distribution")
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", hashlib.sha256(archive.read_bytes()).hexdigest())
    spec = _source(tmp_path / "source")
    manifest = MODULE.create_manifest(
        tmp_path / "source", COMMIT, TREE, spec,
        gradle_archive=archive, gradle_root=root,
    )
    assert manifest["resolution"]["gradle_distribution"]["source"] == "external_verified_archive"


def test_json_output_is_reusable(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    output = tmp_path / "manifest.json"
    arguments = [
        "--source-root", str(tmp_path), "--source-commit", COMMIT, "--source-tree", TREE,
        "--spec", str(spec), "--output", str(output),
    ]
    assert MODULE.main(arguments) == 0
    expected = output.read_text(encoding="utf-8")
    output.write_text("stale\n", encoding="utf-8")
    assert MODULE.main(arguments) == 0
    assert output.read_text(encoding="utf-8") == expected


def test_driver_does_not_keep_the_removed_guard_paths() -> None:
    driver = Path(__file__).with_name("android_build_driver.sh").read_text(encoding="utf-8")
    for removed in (
        "--dependency-closure",
        "DOBBYVPN_REQUIRE_TOOL_CLOSURE",
        "validate_destinations",
        "validate_source_checkout_with_closure",
        "O_EXCL",
        "fsync",
        "chmod 600",
        "trusted-helper",
    ):
        assert removed not in driver
    assert "--gradle-archive" in driver
    assert "verify_source_integrity_after_build" in driver


def test_driver_reuses_the_source_identity_link_for_both_gradle_builds() -> None:
    driver = Path(__file__).with_name("android_build_driver.sh").read_text(encoding="utf-8")
    assert 'source_commit=local' in driver
    assert 'local_source_identity.py' not in driver
    assert 'gradle_flags=(--no-daemon --stacktrace)' in driver
    assert 'source_commit_link="https://github.com/$source_repository/tree/$source_commit"' in driver
    assert driver.count('-PprojectRepositoryCommitLink="$source_commit_link"') == 2
