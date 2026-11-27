"""Verify one TrustTunnel archive against its schema-3 provenance before linking."""

import hashlib
import json
import re
import sys


def verify_bridge(path: str, key: str, archive: str) -> None:
    try:
        with open(path, encoding="utf-8") as stream:
            manifest = json.load(stream)
        if not isinstance(manifest, dict) or manifest.get("schema") != 3:
            raise ValueError("expected schema 3")
        expected = manifest["artifacts"][key]["sha256"]
        if not isinstance(expected, str) or not re.fullmatch(r"[0-9a-f]{64}", expected):
            raise ValueError(f"invalid artifacts.{key}.sha256, expected 64 lowercase hex digits")
        digest = hashlib.sha256()
        with open(archive, "rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    except (OSError, ValueError, TypeError, KeyError) as error:
        raise SystemExit(f"cannot verify TrustTunnel {key} bridge {archive} using {path}: {error}") from error
    actual = digest.hexdigest()
    print(f"TrustTunnel {key} bridge {archive}: expected {expected}, actual {actual}", file=sys.stderr)
    if actual != expected:
        sys.exit(f"TrustTunnel {key} bridge SHA-256 verification failed")


if __name__ == "__main__":
    verify_bridge(*sys.argv[1:])
