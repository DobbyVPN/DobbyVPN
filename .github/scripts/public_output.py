"""Keep public Actions output safe without weakening local diagnostics.

Local invocations continue to print complete child diagnostics.  In GitHub
Actions, complete bytes are first retained in a fresh owner-only runner file;
the public stream receives only a stable kind, status, size, and digest.
"""

from __future__ import annotations

import hashlib
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
from typing import Iterable


SAFE_KIND = re.compile(r"[a-z0-9][a-z0-9_-]{0,47}\Z")
RETENTION_FAILURE_EXIT = 125


def public_actions() -> bool:
    return os.environ.get("GITHUB_ACTIONS", "").lower() == "true"


def _evidence_directory(root_dir: Path) -> Path:
    configured = os.environ.get("RUNNER_TEMP")
    directory = (
        Path(configured) / "dobbyvpn-public-diagnostics"
        if configured
        else root_dir / "runtime" / "public-diagnostics"
    )
    if not directory.is_absolute():
        raise RuntimeError("public diagnostic directory must be absolute")
    _reject_symlink_components(directory)
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    _reject_symlink_components(directory)
    if not directory.is_dir():
        raise RuntimeError("public diagnostic directory is unsafe")
    os.chmod(directory, 0o700)
    return directory


def _reject_symlink_components(path: Path) -> None:
    """Reject a configured evidence path that traverses a symlink."""
    current = Path(path.anchor)
    for component in path.parts[1:]:
        current /= component
        try:
            mode = current.lstat().st_mode
        except FileNotFoundError:
            continue
        if stat.S_ISLNK(mode):
            raise RuntimeError(f"public diagnostic path contains symlink: {current.name}")


def _payload(outputs: Iterable[str | bytes | None]) -> bytes:
    rendered: list[bytes] = []
    for output in outputs:
        if output is None:
            continue
        rendered.append(output if isinstance(output, bytes) else output.encode("utf-8", errors="surrogatepass"))
    return b"".join(rendered)


def emit_diagnostic(kind: str, outputs: Iterable[str | bytes | None], *, root_dir: Path) -> None:
    """Retain complete diagnostics and publish only safe metadata in Actions."""

    if not SAFE_KIND.fullmatch(kind):
        raise ValueError("diagnostic kind contains unsafe metadata")
    payload = _payload(outputs)
    if not payload:
        return
    if not public_actions():
        sys.stderr.buffer.write(payload)
        if not payload.endswith(b"\n"):
            sys.stderr.buffer.write(b"\n")
        sys.stderr.flush()
        return
    directory = _evidence_directory(root_dir)
    descriptor, filename = tempfile.mkstemp(
        prefix=f"{kind}-", suffix=".raw.log", dir=directory
    )
    path = Path(filename)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            descriptor = -1
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(path, 0o600)
    finally:
        if descriptor != -1:
            os.close(descriptor)
    digest = hashlib.sha256(payload).hexdigest()
    print(
        f"dobbyvpn_diagnostic kind={kind} status=retained "
        f"bytes={len(payload)} sha256={digest}",
        file=sys.stderr,
        flush=True,
    )


def main() -> int:
    """Run one workflow command while keeping its original streams private."""
    import argparse

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kind", required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not args.command or args.command[0] != "--":
        parser.error("a command is required after --")
    try:
        completed = subprocess.run(
            args.command[1:], check=False, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )
        payload = (
            b"--- stdout ---\n"
            + completed.stdout
            + b"--- stderr ---\n"
            + completed.stderr
        )
        emit_diagnostic(
            args.kind, (f"exit={completed.returncode}\n", payload), root_dir=Path.cwd()
        )
    except (OSError, RuntimeError, ValueError):
        # Do not expose exception text or permit a workflow's expected
        # non-zero command branch to swallow an evidence-retention failure.
        print(
            f"dobbyvpn_diagnostic kind={args.kind} status=retention-failed",
            file=sys.stderr,
            flush=True,
        )
        return RETENTION_FAILURE_EXIT
    return completed.returncode


if __name__ == "__main__":
    raise SystemExit(main())
