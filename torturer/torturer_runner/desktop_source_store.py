"""Isolate the real desktop account's saved URL for one disposable run."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import stat


def _sync_directory(path: Path) -> None:
    if os.name == "nt":
        return
    try:
        descriptor = os.open(path, os.O_RDONLY)
    except FileNotFoundError:
        return
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _file_state(path: Path) -> dict[str, int | str]:
    metadata = path.stat()
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(64 * 1024), b""):
            digest.update(block)
    return {
        "sha256": digest.hexdigest(),
        "mode": stat.S_IMODE(metadata.st_mode),
        "uid": metadata.st_uid,
        "gid": metadata.st_gid,
    }


def _matches(path: Path, expected: dict[str, int | str] | None) -> bool:
    if expected is None or not path.is_file() or path.is_symlink():
        return False
    return _file_state(path) == expected


def _write_record(record: Path, value: dict, *, initial: bool = False) -> None:
    temporary = record.with_suffix(".updating")
    with temporary.open("x", encoding="utf-8") as stream:
        temporary.chmod(0o600)
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())
    if initial and (record.exists() or record.is_symlink()):
        temporary.unlink()
        raise RuntimeError("desktop source store was already isolated")
    temporary.replace(record)
    _sync_directory(record.parent)


def _read_record(record: Path) -> dict:
    if record.is_symlink():
        raise RuntimeError("desktop source store record is a symlink")
    state = json.loads(record.read_text(encoding="utf-8"))
    if (not isinstance(state, dict)
            or set(state) != {"schema", "existed", "phase", "original"}
            or state["schema"] != 1
            or type(state["existed"]) is not bool
            or state["phase"] not in {"prepared", "isolated"}):
        raise RuntimeError("desktop source store record is invalid")
    original = state["original"]
    if state["existed"]:
        if (not isinstance(original, dict)
                or set(original) != {"sha256", "mode", "uid", "gid"}
                or not isinstance(original["sha256"], str)
                or len(original["sha256"]) != 64
                or any(type(original[key]) is not int for key in ("mode", "uid", "gid"))):
            raise RuntimeError("desktop source store record is invalid")
    elif original is not None:
        raise RuntimeError("desktop source store record is invalid")
    return state


def _remove_record(record: Path) -> None:
    record.unlink(missing_ok=True)
    temporary = record.with_suffix(".updating")
    if temporary.is_symlink():
        raise RuntimeError("desktop source store update record is a symlink")
    temporary.unlink(missing_ok=True)
    _sync_directory(record.parent)


def _source_and_backup(run: Path, home: Path) -> tuple[Path, Path]:
    source = home / ".dobbyvpn" / "configs" / "connection-url.txt"
    backup = run / "source-url-backup"
    for path in (home, source.parent.parent, source.parent, source, backup):
        if path.is_symlink():
            raise RuntimeError(f"desktop source store contains a symlink: {path}")
    return source, backup


def isolate(run: Path, home: Path) -> None:
    record = run / "source-store.json"
    if record.exists() or record.is_symlink():
        raise RuntimeError("desktop source store was already isolated")
    source, backup = _source_and_backup(run, home)
    if backup.exists():
        raise RuntimeError("desktop source URL backup already exists")
    existed = source.exists()
    if existed and not source.is_file():
        raise RuntimeError("saved desktop URL is not a regular file")
    original = _file_state(source) if existed else None
    _write_record(record, {
        "schema": 1, "existed": existed, "phase": "prepared", "original": original,
    }, initial=True)
    if existed:
        # Same-volume rename keeps the original inode, including exact bytes,
        # ownership, and mode. Never copy-delete across filesystems here.
        os.replace(source, backup)
        _sync_directory(source.parent)
        _sync_directory(run)
    _write_record(record, {
        "schema": 1, "existed": existed, "phase": "isolated", "original": original,
    })


def restore(run: Path, home: Path) -> None:
    record = run / "source-store.json"
    temporary = record.with_suffix(".updating")
    source, backup = _source_and_backup(run, home)
    record_exists = record.exists() or record.is_symlink()
    backup_exists = backup.exists() or backup.is_symlink()
    if not record_exists and not backup_exists:
        if temporary.is_symlink():
            raise RuntimeError("desktop source store update record is a symlink")
        temporary.unlink(missing_ok=True)
        return
    state = _read_record(record) if record_exists else None
    if backup.is_symlink():
        raise RuntimeError("desktop source URL backup is a symlink")
    if backup_exists:
        if not backup.is_file():
            raise RuntimeError("desktop source URL backup is not a regular file")
        if state is not None and (
            not state["existed"] or not _matches(backup, state["original"])
        ):
            raise RuntimeError("desktop source URL backup does not match its record")
        source.parent.mkdir(parents=True, exist_ok=True)
        # Atomic replacement both restores the saved inode and makes a retry
        # safe if the process stops before removing the record.
        os.replace(backup, source)
        _sync_directory(source.parent)
        _sync_directory(run)
    elif state is not None:
        if state["existed"]:
            if state["phase"] == "prepared" and _matches(source, state["original"]):
                # The original was never moved; cleanup only removes the
                # incomplete transaction record.
                pass
            elif _matches(source, state["original"]):
                # A prior restore completed its atomic rename but stopped
                # before deleting the transaction record.
                pass
            else:
                raise RuntimeError("original desktop source URL backup is missing")
        elif state["phase"] == "isolated":
            source.unlink(missing_ok=True)
            _sync_directory(source.parent)
        # An absent original in the prepared phase was not isolated yet, so
        # it cannot authorize deleting anything the account currently owns.
    _remove_record(record)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("isolate", "restore"))
    parser.add_argument("--run", type=Path, required=True)
    parser.add_argument("--home", type=Path, required=True)
    args = parser.parse_args()
    if args.operation == "restore":
        restore(args.run, args.home)
    else:
        isolate(args.run, args.home)


if __name__ == "__main__":
    main()
