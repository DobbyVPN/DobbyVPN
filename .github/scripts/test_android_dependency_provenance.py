from __future__ import annotations

import importlib.util
import hashlib
import json
from pathlib import Path
import subprocess
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
        "kind": "dobbyvpn.android.dependency-spec",
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
            wrapper = "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.13-bin.zip\n"
            if wrapper_checksum:
                wrapper += "distributionSha256Sum=" + MODULE.GRADLE_SHA256 + "\n"
            path.write_text(wrapper, encoding="utf-8")
        elif relative == ".go-version":
            path.write_text(MODULE.GO_VERSION + "\n", encoding="utf-8")
        else:
            path.write_bytes(relative.encode("utf-8"))
    spec = root / ".github/android/dependency-spec.json"
    spec.parent.mkdir(parents=True, exist_ok=True)
    spec.write_text(json.dumps(_spec_document(), sort_keys=True) + "\n", encoding="utf-8")
    return spec


def _closure(root: Path) -> Path:
    _source(root)
    directory = root / ".github/android/closure-staging"
    artifacts = directory / "resolved-artifacts"
    cache = directory / "gradle-cache"
    artifacts.mkdir(parents=True, mode=0o700)
    cache.mkdir(parents=True, mode=0o700)
    artifact = artifacts / "com/example/sample/1.0/sample-1.0.jar"
    artifact.parent.mkdir(parents=True, mode=0o700)
    artifact.write_bytes(b"sample artifact\n")
    marker = cache / "gc.properties"
    marker.write_bytes(b"")
    resolver = directory / "gradle-resolution.original.log"
    resolver.write_bytes(b"resolver complete\n")
    verification = directory / "verification-metadata.xml"
    verification.write_bytes(b"<verification-metadata/>\n")
    for path in (artifact, marker, resolver, verification):
        path.chmod(0o600)
    document = {
        "schema": 1,
        "kind": MODULE.CLOSURE_KIND,
        "source_commit": COMMIT,
        "source_tree": TREE,
        "mode": "pinned_network_or_cache",
        "offline_verified": False,
        "artifact_root": "resolved-artifacts",
        "resolved_artifacts": [{
            "coordinate": "com.example:sample:1.0@sample-1.0.jar",
            "url": "https://repo.maven.apache.org/maven2/com/example/sample/1.0/sample-1.0.jar",
            "path": artifact.relative_to(artifacts).as_posix(),
            "sha256": hashlib.sha256(artifact.read_bytes()).hexdigest(),
            "size_bytes": artifact.stat().st_size,
        }],
        "cache_root": "gradle-cache",
        "cache_entries": [{
            "path": "gc.properties",
            "sha256": hashlib.sha256(b"").hexdigest(),
            "size_bytes": 0,
        }],
        "resolution_evidence": {
            "path": resolver.name,
            "sha256": hashlib.sha256(resolver.read_bytes()).hexdigest(),
            "size_bytes": resolver.stat().st_size,
        },
        "verification_metadata": {
            "status": "present",
            "path": verification.name,
            "sha256": hashlib.sha256(verification.read_bytes()).hexdigest(),
            "size_bytes": verification.stat().st_size,
        },
    }
    closure = directory / "dependency-closure.json"
    closure.write_text(json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
    closure.chmod(0o600)
    return closure


def _gradle_distribution(root: Path) -> tuple[Path, Path]:
    root.mkdir(parents=True, exist_ok=True)
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
    with zipfile.ZipFile(archive, "w") as zipped:
        zipped.writestr("gradle-8.13/", b"")
        for relative, content in files.items():
            zipped.writestr(f"gradle-8.13/{relative}", content)
    # The production verifier compares this fixture with the archive bytes.
    # Tests replace the trusted digest with the fixture digest only through a
    # temporary module constant, never in the tracked specification.
    return archive, extracted


@pytest.mark.parametrize(
    "directory_name",
    ["gradle-8.13/../escape/", "../gradle-8.13/", "/gradle-8.13/"],
)
def test_legacy_gradle_proof_rejects_unsafe_directory_entries(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, directory_name: str
) -> None:
    archive, gradle_root = _gradle_distribution(tmp_path)
    with zipfile.ZipFile(archive, "w") as zipped:
        zipped.writestr(directory_name, b"")
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", hashlib.sha256(archive.read_bytes()).hexdigest())
    spec = _source(tmp_path, wrapper_checksum=False)
    with pytest.raises(ValueError, match="unsafe archive path"):
        MODULE.create_manifest(
            tmp_path, COMMIT, TREE, spec, java_version="17.0.16",
            gradle_archive=archive, gradle_root=gradle_root,
        )


def test_legacy_gradle_proof_rejects_duplicate_root_directory_entry(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    archive, gradle_root = _gradle_distribution(tmp_path)
    with zipfile.ZipFile(archive, "a") as zipped:
        zipped.writestr("gradle-8.13/", b"")
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", hashlib.sha256(archive.read_bytes()).hexdigest())
    spec = _source(tmp_path, wrapper_checksum=False)
    with pytest.raises(ValueError, match="duplicate directory"):
        MODULE.create_manifest(
            tmp_path, COMMIT, TREE, spec, java_version="17.0.16",
            gradle_archive=archive, gradle_root=gradle_root,
        )


def test_legacy_gradle_proof_rejects_extra_top_level_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    archive, gradle_root = _gradle_distribution(tmp_path)
    with zipfile.ZipFile(archive, "a") as zipped:
        zipped.writestr("unexpected.txt", b"")
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", hashlib.sha256(archive.read_bytes()).hexdigest())
    spec = _source(tmp_path, wrapper_checksum=False)
    with pytest.raises(ValueError, match="unexpected top-level path"):
        MODULE.create_manifest(
            tmp_path, COMMIT, TREE, spec, java_version="17.0.16",
            gradle_archive=archive, gradle_root=gradle_root,
        )


def test_dependency_manifest_is_deterministic_from_tracked_spec(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    first = MODULE.create_manifest(tmp_path, COMMIT, TREE, spec, java_version="17.0.16")
    second = MODULE.create_manifest(tmp_path, COMMIT, TREE, spec, java_version="17.0.16")
    assert first == second
    assert first["dependency_provenance"] == "tracked_dependency_spec"
    assert first["resolution"]["mode"] == "pinned_network_or_cache"
    assert first["resolution"]["offline_verified"] is False
    assert "no complete offline closure is claimed" in first["resolution"]["evidence"]
    assert first["toolchain"]["java_version"] == "17.0.16"
    assert first["toolchain"]["go_source_commit"] == MODULE.GO_SOURCE_COMMIT
    assert first["go_modules"] == [
        {"module": MODULE.MOBILE_MODULE, "version": MODULE.MOBILE_VERSION, "commands": ["gomobile", "gobind"]}
    ]
    assert first["spec"]["path"] == ".github/android/dependency-spec.json"


def test_owner_closure_manifest_binds_zero_byte_cache_markers(tmp_path: Path) -> None:
    closure = _closure(tmp_path)
    manifest = MODULE.create_closure_manifest(tmp_path, COMMIT, TREE, closure)
    assert manifest["dependency_provenance"] == "complete_owner_evidence"
    assert manifest["resolution"]["cache_entries"] == [{
        "path": "gc.properties",
        "sha256": hashlib.sha256(b"").hexdigest(),
        "size_bytes": 0,
    }]
    assert len(MODULE.closure_staging_paths(tmp_path, closure)) == 5
    manifest_path = tmp_path / "closure-manifest.json"
    manifest_path.write_text(json.dumps(manifest, sort_keys=True) + "\n", encoding="utf-8")
    MODULE.verify_closure_manifest(tmp_path, COMMIT, TREE, closure, manifest_path)


def test_v1_4_8_legacy_wrapper_without_checksum_accepts_verified_distribution(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    archive, gradle_root = _gradle_distribution(tmp_path)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", digest)
    spec = _source(tmp_path, wrapper_checksum=False)
    manifest = MODULE.create_manifest(
        tmp_path,
        COMMIT,
        TREE,
        spec,
        java_version="17.0.16",
        gradle_archive=archive,
        gradle_root=gradle_root,
    )
    proof = manifest["resolution"]["gradle_distribution"]
    assert proof["source"] == "external_verified_archive"
    assert proof["archive_sha256"] == digest
    assert proof["sha256"] == digest
    assert proof["version"] == MODULE.GRADLE_VERSION


def test_legacy_wrapper_without_checksum_requires_external_proof(tmp_path: Path) -> None:
    spec = _source(tmp_path, wrapper_checksum=False)
    with pytest.raises(ValueError, match="externally verified Gradle distribution proof"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, spec, java_version="17.0.16")


def test_current_wrapper_checksum_cannot_be_bypassed_by_external_proof(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    archive, gradle_root = _gradle_distribution(tmp_path)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", digest)
    spec = _source(tmp_path)
    wrapper = tmp_path / "kmp_module/gradle/wrapper/gradle-wrapper.properties"
    wrapper.write_text(
        wrapper.read_text(encoding="utf-8").replace(digest, "0" * 64),
        encoding="utf-8",
    )
    with pytest.raises(ValueError, match="SHA-256"):
        MODULE.create_manifest(
            tmp_path, COMMIT, TREE, spec, java_version="17.0.16",
            gradle_archive=archive, gradle_root=gradle_root,
        )


def test_legacy_gradle_proof_rejects_archive_root_and_url_mismatches(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    archive, gradle_root = _gradle_distribution(tmp_path)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", digest)
    spec = _source(tmp_path, wrapper_checksum=False)
    gradle_wrapper = tmp_path / "kmp_module/gradle/wrapper/gradle-wrapper.properties"

    archive.write_bytes(archive.read_bytes() + b"tampered")
    with pytest.raises(ValueError, match="archive SHA-256"):
        MODULE.create_manifest(
            tmp_path, COMMIT, TREE, spec, java_version="17.0.16",
            gradle_archive=archive, gradle_root=gradle_root,
        )

    archive, gradle_root = _gradle_distribution(tmp_path / "recreated")
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", hashlib.sha256(archive.read_bytes()).hexdigest())
    spec = _source(tmp_path / "recreated", wrapper_checksum=False)
    (gradle_root / "lib/gradle-launcher.jar").write_text("wrong\n", encoding="utf-8")
    with pytest.raises(ValueError, match="root bytes"):
        MODULE.create_manifest(
            tmp_path / "recreated", COMMIT, TREE, spec, java_version="17.0.16",
            gradle_archive=archive, gradle_root=gradle_root,
        )

    archive, gradle_root = _gradle_distribution(tmp_path / "wrong-url")
    monkeypatch.setattr(MODULE, "GRADLE_SHA256", hashlib.sha256(archive.read_bytes()).hexdigest())
    spec = _source(tmp_path / "wrong-url", wrapper_checksum=False)
    gradle_wrapper = tmp_path / "wrong-url/kmp_module/gradle/wrapper/gradle-wrapper.properties"
    gradle_wrapper.write_text(
        gradle_wrapper.read_text(encoding="utf-8").replace(
            "https\\://services.gradle.org/distributions/gradle-8.13-bin.zip",
            "https\\://example.invalid/gradle.zip",
        ),
        encoding="utf-8",
    )
    with pytest.raises(ValueError, match="not the pinned"):
        MODULE.create_manifest(
            tmp_path / "wrong-url", COMMIT, TREE, spec, java_version="17.0.16",
            gradle_archive=archive, gradle_root=gradle_root,
        )


def test_dependency_spec_rejects_a_changed_pin(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    document = json.loads(spec.read_text(encoding="utf-8"))
    document["go_mobile"]["version"] = "v0.0.0-older"
    spec.write_text(json.dumps(document), encoding="utf-8")
    with pytest.raises(ValueError, match="x/mobile pin"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, spec)


def test_dependency_spec_binds_the_source_go_version(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    (tmp_path / ".go-version").write_text("1.25.2\n", encoding="utf-8")
    with pytest.raises(ValueError, match=".go-version"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, spec)


def test_trusted_specification_can_bind_an_old_source_layout(tmp_path: Path) -> None:
    source_spec = _source(tmp_path)
    source_spec.unlink()
    trusted = tmp_path / "trusted"
    trusted_spec = trusted / ".github/android/dependency-spec.json"
    trusted_spec.parent.mkdir(parents=True)
    trusted_spec.write_text(json.dumps(_spec_document()) + "\n", encoding="utf-8")
    manifest = MODULE.create_manifest(
        tmp_path,
        COMMIT,
        TREE,
        trusted_spec,
        trusted_helper_root=trusted,
        trusted_helper_sha="c" * 40,
        java_version="17.0.16",
    )
    assert manifest["spec"]["root"] == "trusted_helper"
    assert manifest["spec"]["trusted_commit"] == "c" * 40
    assert manifest["inputs"][-1]["root"] == "trusted_helper"


def test_manifest_verification_rejects_changed_declared_input(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    manifest_path = tmp_path / "manifest.json"
    manifest = MODULE.create_manifest(tmp_path, COMMIT, TREE, spec, java_version="17.0.16")
    manifest_path.write_text(json.dumps(manifest) + "\n", encoding="utf-8")
    MODULE.verify_manifest(
        tmp_path, COMMIT, TREE, spec, manifest_path, java_version="17.0.16"
    )
    (tmp_path / "kmp_module/settings.gradle.kts").write_bytes(b"changed")
    with pytest.raises(ValueError, match="input hashes changed"):
        MODULE.verify_manifest(
            tmp_path, COMMIT, TREE, spec, manifest_path, java_version="17.0.16"
        )


def test_dependency_spec_rejects_duplicate_json_keys(tmp_path: Path) -> None:
    _source(tmp_path)
    spec = tmp_path / ".github/android/dependency-spec.json"
    spec.write_text('{"schema":1,"schema":1}', encoding="utf-8")
    with pytest.raises(ValueError, match="duplicate JSON key"):
        MODULE._read_spec(spec)


def test_dependency_spec_rejects_a_changed_wrapper_digest(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    wrapper = tmp_path / "kmp_module/gradle/wrapper/gradle-wrapper.properties"
    wrapper.write_text(
        "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.13-bin.zip\n"
        "distributionSha256Sum=" + "0" * 64 + "\n",
        encoding="utf-8",
    )
    with pytest.raises(ValueError, match="SHA-256"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, spec)


def test_dependency_spec_rejects_a_specification_outside_source(tmp_path: Path) -> None:
    _source(tmp_path)
    outside = tmp_path.parent / "outside-dependency-spec.json"
    outside.write_text(json.dumps(_spec_document()), encoding="utf-8")
    with pytest.raises(ValueError, match="beneath"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, outside)


def test_dependency_spec_rejects_declared_input_symlink(tmp_path: Path) -> None:
    spec = _source(tmp_path)
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "settings.gradle.kts").write_bytes(b"outside")
    settings = tmp_path / "kmp_module/settings.gradle.kts"
    settings.unlink()
    settings.symlink_to(outside / "settings.gradle.kts")
    with pytest.raises(ValueError, match="symlink"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, spec)


def test_print_mobile_version_uses_the_spec_pin(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    spec = _source(tmp_path)
    assert MODULE.main(["--spec", str(spec), "--print-mobile-version"]) == 0
    assert capsys.readouterr().out == f"{MODULE.MOBILE_MODULE}@{MODULE.MOBILE_VERSION}\n"


def test_print_go_source_commit_uses_the_spec_pin(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    spec = _source(tmp_path)
    assert MODULE.main(["--spec", str(spec), "--print-go-source-commit"]) == 0
    assert capsys.readouterr().out == f"{MODULE.GO_SOURCE_COMMIT}\n"


def test_print_go_version_uses_the_spec_pin(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    spec = _source(tmp_path)
    assert MODULE.main(["--spec", str(spec), "--print-go-version"]) == 0
    assert capsys.readouterr().out == f"{MODULE.GO_VERSION}\n"


def _git_repository(root: Path, *, content: str = "clean\n") -> str:
    root.mkdir(parents=True, exist_ok=True)
    subprocess.run(["git", "init", str(root)], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.email", "test@example.invalid"], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.name", "Android test"], check=True)
    (root / "content.txt").write_text(content, encoding="utf-8")
    subprocess.run(["git", "-C", str(root), "add", "-A"], check=True)
    subprocess.run(["git", "-C", str(root), "commit", "-m", "fixture"], check=True)
    return subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()


def _trusted_helper_repository(root: Path) -> str:
    for relative in (
        ".github/scripts/android_build_driver.sh",
        ".github/scripts/android_dependency_provenance.py",
        ".github/scripts/public_output.py",
        ".github/scripts/verify_android_apk_source.py",
        ".github/scripts/verify_android_reproducibility.py",
    ):
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("trusted helper\n", encoding="utf-8")
        if path.name == "android_build_driver.sh":
            path.chmod(0o755)
    spec = root / ".github/android/dependency-spec.json"
    spec.parent.mkdir(parents=True, exist_ok=True)
    spec.write_text(json.dumps(_spec_document()) + "\n", encoding="utf-8")
    return _git_repository(root)


def _validate_driver_contract(source: Path, trusted: Path, sha: str, *extra: str) -> int:
    driver = Path(__file__).with_name("android_build_driver.sh")
    return subprocess.run(
        [
            str(driver),
            "--validate-helper-contract",
            "--source-root",
            str(source),
            "--trusted-helper-root",
            str(trusted),
            "--trusted-helper-sha",
            sha,
            *extra,
        ],
        check=False,
    ).returncode


def test_driver_helper_contract_validation_skips_build_destination_validation(tmp_path: Path) -> None:
    """Contract validation intentionally supplies no build output paths."""
    source = tmp_path / "legacy-source"
    trusted = tmp_path / "trusted"
    _git_repository(source)
    trusted_sha = _trusted_helper_repository(trusted)
    assert _validate_driver_contract(source, trusted, trusted_sha) == 0


def test_driver_rejects_unbound_helper_override(tmp_path: Path) -> None:
    source = tmp_path / "legacy-source"
    trusted = tmp_path / "trusted"
    _git_repository(source)
    trusted_sha = _trusted_helper_repository(trusted)
    outside = tmp_path / "outside.py"
    outside.write_text("outside\n", encoding="utf-8")
    assert _validate_driver_contract(
        source, trusted, trusted_sha, "--dependency-helper", str(outside)
    ) != 0


def test_driver_rejects_symlink_dirty_and_wrong_sha_helper_roots(tmp_path: Path) -> None:
    source = tmp_path / "legacy-source"
    trusted = tmp_path / "trusted"
    _git_repository(source)
    trusted_sha = _trusted_helper_repository(trusted)
    symlink = tmp_path / "trusted-symlink"
    symlink.symlink_to(trusted, target_is_directory=True)
    assert _validate_driver_contract(source, symlink, trusted_sha) != 0
    assert _validate_driver_contract(source, trusted, "0" * 40) != 0
    (trusted / "content.txt").write_text("dirty\n", encoding="utf-8")
    assert _validate_driver_contract(source, trusted, trusted_sha) != 0


def test_public_driver_uses_spec_and_builds_missing_pinned_tools() -> None:
    driver = Path(__file__).with_name("android_build_driver.sh").read_text(encoding="utf-8")
    assert "--dependency-spec" in driver
    assert "--dependency-closure" in driver
    assert "--closure-evidence" in driver
    assert "ensure_mobile_tool" in driver
    assert 'GOBIN="$go_path/bin" "$go_bin" install' in driver
    assert "tracked_dependency_spec" in driver
    assert "complete_owner_evidence" in driver
    assert "--trusted-helper-root" in driver
    assert "--trusted-helper-sha" in driver
    assert "verify_source_integrity_after_build" in driver
    assert "--verify-manifest" in driver


def test_public_workflow_derives_the_tool_pin_from_the_spec() -> None:
    workflow = Path(__file__).resolve().parents[1] / "workflows/android_build.yml"
    text = workflow.read_text(encoding="utf-8")
    assert "dependency-spec.json" in text
    assert "GOBIN=\"$GOPATH/bin\" go install" in text
    assert "dependency-closure" not in text
    assert "ref: ${{ github.workflow_sha }}" in text
    assert "TRUSTED_HELPER_SHA: ${{ github.workflow_sha }}" in text
    assert "--print-go-source-commit" in text
