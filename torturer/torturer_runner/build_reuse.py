"""Identity and validation for disposable, retained development packages."""

from __future__ import annotations

import hashlib
import os
from pathlib import Path, PurePosixPath
import re
from typing import Any

_SHA = re.compile(r"[0-9a-f]{40}\Z")
_DIGEST = re.compile(r"[0-9a-f]{64}\Z")
_GENERATED = {".git", ".gradle", ".build", ".swiftpm", "build", "bin", "obj",
              "__pycache__", "DerivedData", "node_modules", ".venv", "venv", ".kotlin",
              ".pytest_cache"}
_ROOT_GENERATED = {".local-tools", "output", "runtime", "android_module", "go_module", "reports"}
_TEST_DIRECTORIES = {"tests", "test", "testdata", "androidTest"}
_TEST_SCRIPTS = {"source_checks.py", "check_production_mutations.py",
                 "check_presentation_mutations.py"}


def production_inputs(source: Path) -> str:
    """Hash package inputs, excluding test implementations and generated files.

    This deliberately includes all frontends and build scripts. A false reuse
    rejection costs a build; accepting a changed production input tests the
    wrong application. The same implementation runs on controller and guest.
    """
    digest = hashlib.sha256()
    files: list[Path] = []
    for directory, children, names in os.walk(source):
        relative = Path(directory).relative_to(source)
        children[:] = sorted(name for name in children if name not in _GENERATED
                             and name not in _TEST_DIRECTORIES
                             and not (relative == Path(".") and name in {"torturer", *_ROOT_GENERATED})
                             and not (relative == Path("core") and name == "runtime"))
        for name in names:
            if name in _GENERATED:
                continue
            path = Path(directory) / name
            parts = path.relative_to(source).parts
            if (name.endswith(("_test.go", ".pyc")) or name == "README.md"
                    or name in _TEST_SCRIPTS or name.startswith("test_") and name.endswith(".py")
                    or "UITests" in parts or "NativeAppUITests" in parts
                    or (parts[:2] == (".github", "workflows") and name != "build.yml")):
                continue
            if parts[0] == "core" and name in {
                "libdobby_bridge.so", "libc++.so.1", "libc++abi.so.1",
            }:
                continue
            if path.is_symlink():
                raise ValueError(f"production input is a symlink: {path.relative_to(source)}")
            files.append(path)
    if not files:
        raise ValueError("production input tree is empty")
    for path in sorted(files, key=lambda item: item.relative_to(source).as_posix()):
        name = path.relative_to(source).as_posix().encode("utf-8")
        digest.update(len(name).to_bytes(8, "big"))
        digest.update(name)
        digest.update(bytes.fromhex(file_sha256(path)))
    return digest.hexdigest()


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def validate_reuse_manifest(source: Path, run_dir: Path, manifest: dict[str, Any],
                            platform: str, architecture: str) -> Path:
    """Validate the selected package before any guest installation."""
    if manifest.get("schema") != 1 or manifest.get("mode") != "local-reuse":
        raise ValueError("reuse manifest schema or mode is invalid")
    if manifest.get("platform") != platform:
        raise ValueError("reuse platform differs from the guest")
    for key in ("app_sha", "test_sha"):
        if not isinstance(manifest.get(key), str) or not _SHA.fullmatch(manifest[key]):
            raise ValueError(f"reuse {key} is invalid")
    if manifest.get("production_inputs") != production_inputs(source):
        raise ValueError("production inputs changed; build a new application")
    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, list) or len(artifacts) != 1:
        raise ValueError("reuse requires one selected platform package")
    artifact = artifacts[0]
    if not isinstance(artifact, dict) or artifact.get("role") != "package":
        raise ValueError("reuse artifact role is invalid")
    if artifact.get("architecture") != architecture:
        raise ValueError("reuse architecture differs from the guest")
    relative = artifact.get("relative_path")
    if not isinstance(relative, str):
        raise ValueError("reuse artifact path is invalid")
    parts = PurePosixPath(relative).parts
    if (not parts or PurePosixPath(relative).is_absolute()
            or any(part in {".", ".."} for part in relative.split("/"))
            or "\\" in relative):
        raise ValueError("reuse artifact path is invalid")
    path = run_dir.joinpath(*parts)
    if any(parent.is_symlink() for parent in [path, *path.parents] if parent != run_dir.parent):
        raise ValueError("reuse artifact path contains a symlink")
    if path.name != artifact.get("file_name") or not path.is_file():
        raise ValueError("reuse artifact file is missing or misidentified")
    expected = artifact.get("sha256")
    if not isinstance(expected, str) or not _DIGEST.fullmatch(expected):
        raise ValueError("reuse artifact hash is invalid")
    if file_sha256(path) != expected:
        raise ValueError("reuse artifact hash mismatch")
    return path
