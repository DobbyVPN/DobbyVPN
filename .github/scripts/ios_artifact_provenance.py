#!/usr/bin/env python3
"""Create and verify the public provenance sidecar for a release IPA.

The sidecar is deliberately small, canonical JSON.  It binds the one IPA
published by an iOS Release job to its immutable source revision and release
metadata before the protected App Store submission job is allowed to use it.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
from typing import Any


SCHEMA = "dobbyvpn.ios-artifact-provenance.v1"
FULL_LOWER_SHA = re.compile(r"[0-9a-f]{40}")
SEMANTIC_VERSION = re.compile(r"(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)")
POSITIVE_INTEGER = re.compile(r"[1-9]\d*")
SHA256 = re.compile(r"[0-9a-f]{64}")


def canonical_json(value: dict[str, Any]) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True) + "\n").encode("utf-8")


def require_regular_file(path: Path, description: str) -> None:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"{description} must be a regular non-symlink file: {path}")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_source_sha(value: str) -> str:
    if not FULL_LOWER_SHA.fullmatch(value):
        raise ValueError("source_sha must be a lowercase full 40-character commit SHA")
    return value


def validate_version(value: str) -> str:
    if not SEMANTIC_VERSION.fullmatch(value):
        raise ValueError("version must use canonical numeric X.Y.Z format")
    return value


def validate_build_number(value: str | int) -> int:
    text = str(value)
    if not POSITIVE_INTEGER.fullmatch(text):
        raise ValueError("build_number must be a positive canonical integer")
    return int(text)


def discover_ipa(ipa_dir: Path) -> Path:
    if ipa_dir.is_symlink() or not ipa_dir.is_dir():
        raise ValueError(f"IPA directory must be a real directory: {ipa_dir}")
    candidates = sorted(path for path in ipa_dir.iterdir() if path.name.endswith(".ipa"))
    if len(candidates) != 1:
        raise ValueError(f"expected exactly one IPA in {ipa_dir}, found {len(candidates)}")
    ipa = candidates[0]
    require_regular_file(ipa, "IPA")
    return ipa


def make_provenance(ipa_dir: Path, source_sha: str, version: str, build_number: str | int) -> dict[str, Any]:
    ipa = discover_ipa(ipa_dir)
    size_bytes = ipa.stat().st_size
    if size_bytes <= 0:
        raise ValueError("IPA must not be empty")
    return {
        "build_number": validate_build_number(build_number),
        "ipa": {
            "filename": ipa.name,
            "sha256": sha256_file(ipa),
            "size_bytes": size_bytes,
        },
        "schema": SCHEMA,
        "source_sha": validate_source_sha(source_sha),
        "version": validate_version(version),
    }


def load_provenance(path: Path) -> dict[str, Any]:
    require_regular_file(path, "provenance sidecar")
    raw = path.read_bytes()
    try:
        value = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"provenance sidecar is not valid UTF-8 JSON: {error}") from error
    if not isinstance(value, dict) or set(value) != {"schema", "source_sha", "version", "build_number", "ipa"}:
        raise ValueError("provenance sidecar has an unexpected schema")
    ipa = value.get("ipa")
    if not isinstance(ipa, dict) or set(ipa) != {"filename", "size_bytes", "sha256"}:
        raise ValueError("provenance sidecar has an unexpected ipa schema")
    if value["schema"] != SCHEMA:
        raise ValueError("provenance sidecar schema is unsupported")
    if not isinstance(value["source_sha"], str):
        raise ValueError("provenance source_sha must be a string")
    if not isinstance(value["version"], str):
        raise ValueError("provenance version must be a string")
    if isinstance(value["build_number"], bool) or not isinstance(value["build_number"], int):
        raise ValueError("provenance build_number must be an integer")
    if not isinstance(ipa["filename"], str) or Path(ipa["filename"]).name != ipa["filename"] or not ipa["filename"].endswith(".ipa"):
        raise ValueError("provenance IPA filename is invalid")
    if isinstance(ipa["size_bytes"], bool) or not isinstance(ipa["size_bytes"], int) or ipa["size_bytes"] <= 0:
        raise ValueError("provenance IPA size must be a positive integer")
    if not isinstance(ipa["sha256"], str) or not SHA256.fullmatch(ipa["sha256"]):
        raise ValueError("provenance IPA SHA-256 must be lowercase hexadecimal")
    validate_source_sha(value["source_sha"])
    validate_version(value["version"])
    validate_build_number(value["build_number"])
    if raw != canonical_json(value):
        raise ValueError("provenance sidecar must use canonical JSON encoding")
    return value


def verify_provenance(
    ipa_dir: Path,
    provenance_path: Path,
    source_sha: str,
    version: str,
    build_number: str | int,
) -> None:
    expected = make_provenance(ipa_dir, source_sha, version, build_number)
    actual = load_provenance(provenance_path)
    if actual != expected:
        raise ValueError("IPA provenance does not match the selected source, version, build, filename, size, and digest")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("create", "verify"):
        command = commands.add_parser(name)
        command.add_argument("--ipa-dir", type=Path, required=True)
        command.add_argument("--provenance", type=Path, required=True)
        command.add_argument("--source-sha", required=True)
        command.add_argument("--version", required=True)
        command.add_argument("--build-number", required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        if args.command == "create":
            args.provenance.parent.mkdir(parents=True, exist_ok=True)
            if args.provenance.is_symlink():
                raise ValueError(f"provenance output must not be a symlink: {args.provenance}")
            args.provenance.write_bytes(
                canonical_json(make_provenance(args.ipa_dir, args.source_sha, args.version, args.build_number))
            )
        else:
            verify_provenance(args.ipa_dir, args.provenance, args.source_sha, args.version, args.build_number)
    except (OSError, ValueError) as error:
        print(f"ios artifact provenance error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
