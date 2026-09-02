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
import stat
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
    relative = path.relative_to(root).as_posix()
    if (
        not relative
        or relative.startswith("/")
        or any(character in relative for character in ("\x00", "\n", "\r", "\\"))
    ):
        raise IdentityError("source tree contains a non-canonical path")
    try:
        relative.encode("utf-8")
    except UnicodeEncodeError as error:
        raise IdentityError("source tree contains a non-UTF-8 path") from error
    return relative


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


def _digest_file(path: Path) -> tuple[int, int, str]:
    before = path.lstat()
    if stat.S_ISLNK(before.st_mode) or not stat.S_ISREG(before.st_mode):
        raise IdentityError(f"source tree contains a non-regular entry: {path}")
    digest = hashlib.sha256()
    size = 0
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as error:
        raise IdentityError(f"could not open source file: {path}") from error
    try:
        opened = os.fstat(descriptor)
        if (
            opened.st_dev != before.st_dev
            or opened.st_ino != before.st_ino
            or opened.st_size != before.st_size
        ):
            raise IdentityError(f"source file changed while being inspected: {path}")
        with os.fdopen(descriptor, "rb", closefd=False) as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                size += len(chunk)
                digest.update(chunk)
    finally:
        os.close(descriptor)
    after = path.lstat()
    if (
        after.st_dev != before.st_dev
        or after.st_ino != before.st_ino
        or after.st_size != size
        or stat.S_IMODE(after.st_mode) != stat.S_IMODE(before.st_mode)
    ):
        raise IdentityError(f"source file changed while being inspected: {path}")
    return stat.S_IMODE(before.st_mode), size, digest.hexdigest()


def _records(root: Path, exclusions: frozenset[str]) -> list[dict[str, object]]:
    if root.is_symlink() or not root.is_dir():
        raise IdentityError("source root must be a real directory")
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
            try:
                mode = entry.stat(follow_symlinks=False).st_mode
            except OSError as error:
                raise IdentityError(f"could not inspect source entry: {path}") from error
            if stat.S_ISLNK(mode):
                raise IdentityError(f"source tree contains a symlink: {path}")
            if stat.S_ISDIR(mode):
                visit(path)
                continue
            if not stat.S_ISREG(mode):
                raise IdentityError(f"source tree contains a non-regular entry: {path}")
            file_mode, size, digest = _digest_file(path)
            records.append(
                {
                    "mode": file_mode,
                    "path": relative.as_posix(),
                    "sha256": digest,
                    "size_bytes": size,
                }
            )

    visit(root)
    return records


def content_identities(root: Path, exclusions: frozenset[str] = frozenset()) -> tuple[str, str]:
    if root.is_symlink():
        raise IdentityError("source root must be a real directory")
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
        help="canonical generated file or directory path relative to --root",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        exclusions: set[str] = set()
        for value in args.exclude:
            relative = Path(value)
            if (
                not value
                or any(character in value for character in ("\x00", "\n", "\r", "\\"))
                or relative.is_absolute()
                or relative.as_posix() != value
                or any(part in {"", ".", ".."} for part in relative.parts)
            ):
                raise IdentityError("--exclude must be a canonical relative path")
            exclusions.add(value)
        commit, tree = content_identities(args.root, frozenset(exclusions))
    except (IdentityError, OSError, RuntimeError) as error:
        print(f"local source identity failed: {error}", file=sys.stderr)
        return 2
    print(f"{commit} {tree}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
