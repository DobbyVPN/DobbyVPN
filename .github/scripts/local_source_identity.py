#!/usr/bin/env python3
"""Compute the identity of an extracted, dirty local source tree.

This is deliberately a local-only identity.  It is not a Git commit and is
never accepted by a Release build.  The Android local candidate path uses it
because the Harness streams a worktree without its ``.git`` directory.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import sys


# These are generated or owner-local roots, not source inputs.  Keep this list
# small and explicit: a newly-created path outside it must change the local
# identity (or be supplied as an explicit generated output by the driver).
FIXED_GENERATED_ROOTS = frozenset(
    {
        ".android-build",
        ".dobbyvpn-local-candidate",
        ".git",
        "runtime",
    }
)
KMP_GENERATED_DIRECTORY_NAMES = frozenset({"build", ".gradle", ".kotlin"})


class IdentityError(ValueError):
    pass


def _relative_path(root: Path, path: Path) -> str:
    return path.relative_to(root).as_posix()


def _is_excluded(relative: Path, exclusions: frozenset[str]) -> bool:
    if not relative.parts:
        return False
    text = relative.as_posix()
    if text in exclusions or relative.parts[0] in FIXED_GENERATED_ROOTS:
        return True
    # Gradle may create state at kmp_module/build or below an individual
    # module.  Only these known generated names below kmp_module are skipped.
    if relative.parts[0] == "kmp_module" and any(
        part in KMP_GENERATED_DIRECTORY_NAMES for part in relative.parts[1:]
    ):
        return True
    return False


def _digest_file(path: Path) -> tuple[bool, int, str]:
    info = path.stat()
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            size += len(chunk)
            digest.update(chunk)
    return bool(info.st_mode & 0o111), size, digest.hexdigest()


def _records(root: Path, exclusions: frozenset[str]) -> list[dict[str, object]]:
    if not root.is_dir():
        raise IdentityError("source root must be a directory")
    records: list[dict[str, object]] = []

    def visit(directory: Path) -> None:
        try:
            entries = sorted(os.scandir(directory), key=lambda entry: entry.name)
        except OSError as error:
            raise IdentityError(f"could not read source directory: {directory}") from error
        for entry in entries:
            path = Path(entry.path)
            relative = Path(_relative_path(root, path))
            if _is_excluded(relative, exclusions):
                continue
            if entry.is_symlink():
                records.append(
                    {
                        "path": relative.as_posix(),
                        "symlink": os.readlink(path),
                    }
                )
                continue
            if entry.is_dir(follow_symlinks=False):
                visit(path)
                continue
            if entry.is_file(follow_symlinks=False):
                executable, size, digest = _digest_file(path)
                records.append(
                    {
                        "executable": executable,
                        "path": relative.as_posix(),
                        "sha256": digest,
                        "size_bytes": size,
                    }
                )
            else:
                records.append({"path": relative.as_posix(), "type": "other"})

    visit(root)
    return records


def content_identities(root: Path, exclusions: frozenset[str] = frozenset()) -> tuple[str, str]:
    root = root.resolve(strict=True)
    records = _records(root, exclusions)
    encoded = json.dumps(
        {"schema": 1, "entries": records},
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")

    def identity(domain: bytes) -> str:
        digest = hashlib.blake2s(domain + encoded, digest_size=20)
        return digest.hexdigest()

    return identity(b"dobbyvpn-local-content\0"), identity(b"dobbyvpn-local-tree\0")


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument(
        "--exclude",
        action="append",
        default=[],
        help="generated file or directory path relative to --root",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        commit, tree = content_identities(args.root, frozenset(args.exclude))
    except (IdentityError, OSError, RuntimeError) as error:
        print(f"local source identity failed: {error}", file=sys.stderr)
        return 2
    print(f"{commit} {tree}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
