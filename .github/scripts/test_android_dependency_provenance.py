from __future__ import annotations

import hashlib
import importlib.util
import json
import subprocess
import sys
from pathlib import Path

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


def _source(root: Path) -> None:
    for relative in MODULE.DECLARED_INPUTS:
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        if relative.endswith("gradle-wrapper.properties"):
            path.write_text(
                "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.13-bin.zip\n"
                "distributionSha256Sum=" + "1" * 64 + "\n",
                encoding="utf-8",
            )
        else:
            path.write_bytes(relative.encode("utf-8"))


def _closure(
    root: Path,
    *,
    artifacts: list[dict[str, object]] | None = None,
    staged: bool = False,
) -> Path:
    base = root / "closure-staging" if staged else root
    log = base / "resolver.log"
    base.mkdir(parents=True, exist_ok=True)
    log.write_bytes(b"pinned resolver evidence\n")
    verification = base / "verification.json"
    verification.write_bytes(b'{"status":"present","method":"owner-pinned-verifier-v1"}\n')
    artifact_root = base / "resolved-artifacts"
    artifact_root.mkdir()
    closure = base / "dependency-closure.json"
    records = list(artifacts) if artifacts is not None else [
        {
            "coordinate": "com.example:demo:1.0",
            "url": "https://repo.maven.apache.org/maven2/com/example/demo/1.0/demo-1.0.pom",
            "path": "com/example/demo/1.0/demo-1.0.pom",
        }
    ]
    normalized_records: list[dict[str, object]] = []
    for index, item in enumerate(records):
        normalized = dict(item)
        relative = normalized.get("path", f"artifact-{index}.bin")
        path = artifact_root / str(relative)
        path.parent.mkdir(parents=True, exist_ok=True)
        content = normalized.pop("_content", f"artifact-{index}\n".encode())
        if isinstance(content, str):
            content = content.encode()
        path.write_bytes(content)
        normalized["path"] = relative
        normalized["sha256"] = hashlib.sha256(content).hexdigest()
        normalized["size_bytes"] = len(content)
        normalized_records.append(normalized)
    document = {
        "schema": 1,
        "kind": "dobbyvpn.android.dependency-closure",
        "source_commit": COMMIT,
        "source_tree": TREE,
        "mode": "pinned_network_or_cache",
        "offline_verified": False,
        "artifact_root": artifact_root.name,
        "resolved_artifacts": normalized_records,
        "resolution_evidence": {
            "path": log.name,
            "sha256": hashlib.sha256(log.read_bytes()).hexdigest(),
            "size_bytes": log.stat().st_size,
        },
        "verification_metadata": {
            "status": "present",
            "path": verification.name,
            "sha256": hashlib.sha256(verification.read_bytes()).hexdigest(),
            "size_bytes": verification.stat().st_size,
        },
    }
    closure.write_text(json.dumps(document, sort_keys=True), encoding="utf-8")
    return closure


def test_dependency_manifest_requires_byte_addressed_resolution_evidence(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path)
    first = MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)
    second = MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)
    assert first == second
    assert first["dependency_provenance"] == "complete_owner_evidence"
    assert first["resolution"]["offline_verified"] is False
    assert first["resolution"]["resolved_artifacts"]
    assert first["resolution"]["resolution_evidence"]["sha256"] == hashlib.sha256(
        (tmp_path / "resolver.log").read_bytes()
    ).hexdigest()


def test_dependency_manifest_rejects_declarative_empty_closure(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path, artifacts=[])
    with pytest.raises(ValueError, match="enumerate resolved artifacts"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_rejects_tampered_resolution_log(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path)
    (tmp_path / "resolver.log").write_bytes(b"changed\n")
    with pytest.raises(ValueError, match="resolver log bytes"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_rejects_tampered_resolved_artifact_bytes(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path)
    (tmp_path / "resolved-artifacts/com/example/demo/1.0/demo-1.0.pom").write_bytes(b"tampered")
    with pytest.raises(ValueError, match="artifact bytes"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_rejects_duplicate_coordinate_url_path(tmp_path: Path) -> None:
    _source(tmp_path)
    for field, records in (
        (
            "coordinate",
            [
                {"coordinate": "same:demo:1", "url": "https://example.invalid/a"},
                {"coordinate": "same:demo:1", "url": "https://example.invalid/b"},
            ],
        ),
        (
            "url",
            [
                {"coordinate": "a:demo:1", "url": "https://example.invalid/same"},
                {"coordinate": "b:demo:1", "url": "https://example.invalid/same"},
            ],
        ),
        (
            "path",
            [
                {"coordinate": "a:demo:1", "url": "https://example.invalid/a", "path": "same.bin", "_content": b"same"},
                {"coordinate": "b:demo:1", "url": "https://example.invalid/b", "path": "same.bin", "_content": b"same"},
            ],
        ),
    ):
        case_root = tmp_path / field
        case_root.mkdir()
        _source(case_root)
        case_closure = _closure(case_root, artifacts=records)
        with pytest.raises(ValueError, match="duplicate|unique"):
            MODULE.create_manifest(case_root, COMMIT, TREE, case_closure)


def test_dependency_manifest_accepts_identical_bytes_for_distinct_artifacts(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path, artifacts=[
        {"coordinate": "a:demo:1", "url": "https://example.invalid/a", "_content": b"same"},
        {"coordinate": "b:demo:1", "url": "https://example.invalid/b", "_content": b"same"},
    ])
    manifest = MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)
    assert len(manifest["resolution"]["resolved_artifacts"]) == 2


def test_dependency_json_rejects_exponent_overflow_recursively(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path)
    document = closure.read_text(encoding="utf-8")
    document = document.replace(
        '"status": "present"',
        '"status": "present", "nested": {"overflow": 1e9999}',
    )
    closure.write_text(document, encoding="utf-8")
    with pytest.raises(ValueError, match="non-finite JSON number"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_rejects_noncanonical_or_escaping_artifact_path(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path, artifacts=[
        {
            "coordinate": "a:demo:1",
            "url": "https://example.invalid/a",
            "path": "../escape.bin",
        }
    ])
    with pytest.raises(ValueError, match="canonical relative path"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_rejects_declared_input_ancestor_symlink(tmp_path: Path) -> None:
    _source(tmp_path)
    real = tmp_path / "outside-inputs"
    real.mkdir()
    target = real / "settings.gradle.kts"
    target.write_bytes(b"outside source bytes")
    original = tmp_path / "kmp_module"
    # Replace only the declared-input ancestor with a symlink.  The helper
    # must reject the traversal before hashing the redirected file.
    import shutil
    shutil.rmtree(original)
    original.symlink_to(real, target_is_directory=True)
    closure = _closure(tmp_path)
    with pytest.raises(ValueError, match="symlink|escapes"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_rejects_gradle_url_suffix_or_query(tmp_path: Path) -> None:
    _source(tmp_path)
    wrapper = tmp_path / "kmp_module/gradle/wrapper/gradle-wrapper.properties"
    wrapper.write_text(
        "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.13-bin.zip?cache=1\n"
        "distributionSha256Sum=" + "1" * 64 + "\n",
        encoding="utf-8",
    )
    closure = _closure(tmp_path)
    with pytest.raises(ValueError, match="pinned 8.13"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_rejects_artifact_symlink_traversal(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path)
    artifact = tmp_path / "resolved-artifacts/com/example/demo/1.0/demo-1.0.pom"
    outside = tmp_path / "outside.bin"
    outside.write_bytes(artifact.read_bytes())
    artifact.unlink()
    artifact.symlink_to(outside)
    with pytest.raises(ValueError, match="symlink"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_dependency_manifest_requires_byte_bound_verification_metadata(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path)
    document = json.loads(closure.read_text())
    document["verification_metadata"] = {"status": "present"}
    closure.write_text(json.dumps(document, sort_keys=True), encoding="utf-8")
    with pytest.raises(ValueError, match="verification metadata identity"):
        MODULE.create_manifest(tmp_path, COMMIT, TREE, closure)


def test_closure_staging_paths_are_exact_and_dedicated(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path, staged=True)
    (closure.parent / "undeclared.log").write_bytes(b"must not be admitted\n")
    paths = set(MODULE.closure_staging_paths(tmp_path, closure))
    assert paths == {
        "closure-staging/dependency-closure.json",
        "closure-staging/resolver.log",
        "closure-staging/verification.json",
        "closure-staging/resolved-artifacts/com/example/demo/1.0/demo-1.0.pom",
    }
    assert "closure-staging/undeclared.log" not in paths


def test_closure_staging_cli_prints_only_declared_paths(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path, staged=True)
    result = subprocess.run(
        [
            sys.executable,
            str(Path(MODULE.__file__)),
            "--source-root",
            str(tmp_path),
            "--closure-evidence",
            str(closure),
            "--print-staged-paths",
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stderr
    assert set(result.stdout.splitlines()) == set(MODULE.closure_staging_paths(tmp_path, closure))


def test_closure_staging_paths_rejects_closure_at_source_root(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path)
    with pytest.raises(ValueError, match="dedicated source directory"):
        MODULE.closure_staging_paths(tmp_path, closure)


def test_closure_staging_paths_rejects_declared_path_collision(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path, staged=True)
    document = json.loads(closure.read_text(encoding="utf-8"))
    document["verification_metadata"]["path"] = "resolver.log"
    closure.write_text(json.dumps(document, sort_keys=True), encoding="utf-8")
    with pytest.raises(ValueError, match="same path twice"):
        MODULE.closure_staging_paths(tmp_path, closure)


def test_closure_staging_paths_rejects_artifact_root_symlink(tmp_path: Path) -> None:
    _source(tmp_path)
    closure = _closure(tmp_path, staged=True)
    import shutil

    artifact_root = closure.parent / "resolved-artifacts"
    shutil.rmtree(artifact_root)
    outside = tmp_path / "outside-artifacts"
    outside.mkdir()
    artifact_root.symlink_to(outside, target_is_directory=True)
    with pytest.raises(ValueError, match="artifact_root.*(real directory|symlink)"):
        MODULE.closure_staging_paths(tmp_path, closure)
