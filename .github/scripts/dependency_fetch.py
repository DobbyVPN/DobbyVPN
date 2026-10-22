#!/usr/bin/env python3
"""Retry narrowly scoped dependency fetches and cache verified archives."""
from __future__ import annotations

import argparse
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import traceback
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))
from bounded_process import (
    command_deadline_remaining,
    exception_output,
    output_text,
    run_finite_capture,
)

MAX_ATTEMPTS = 3
RETRY_DELAYS = (2, 5)
TOOL_CACHE_ENV = "DOBBYVPN_PINNED_TOOL_CACHE"

_AUTH_ERROR = re.compile(
    r"(?:\bHTTP(?:/\d+(?:\.\d+)?)?\s+(?:Error\s*)?(?:401|403|407|511)\b|"
    r"\b(?:401\s+unauthorized|403\s+forbidden|unauthorized|forbidden|"
    r"authentication (?:failed|required)|invalid credentials|bad credentials)\b)",
    re.IGNORECASE,
)
_INVALID_OR_CHECKSUM = re.compile(
    r"(?:checksum|sha-?256|go\.sum|security error|invalid version|"
    r"unknown revision|no matching versions|version is invalid)",
    re.IGNORECASE,
)
_THROTTLED = re.compile(
    r"(?:\bHTTP(?:/\d+(?:\.\d+)?)?\s+(?:Error\s*)?429\b|"
    r"\b(?:status|response|returned)(?:\s+(?:status\s+)?code)?\s*[:=]?\s*429\b|"
    r"\breturned error:\s*429\b|"
    r"too many requests|rate[ -]?limit(?:ed|ing)?|throttl(?:e|ed|ing))",
    re.IGNORECASE,
)
_SERVER_ERROR = re.compile(
    r"(?:\bHTTP(?:/\d+(?:\.\d+)?)?\s+(?:Error\s*)?5[0-9]{2}\b|"
    r"\b(?:status|response|returned)(?:\s+(?:status\s+)?code)?\s*[:=]?\s*5[0-9]{2}\b|"
    r"\breturned error:\s*5[0-9]{2}\b|"
    r"\b(?:500\s+Internal Server Error|501\s+Not Implemented|"
    r"502\s+Bad Gateway|503\s+Service Unavailable|504\s+Gateway Timeout|"
    r"505\s+HTTP Version Not Supported|506\s+Variant Also Negotiates|"
    r"507\s+Insufficient Storage|508\s+Loop Detected|"
    r"510\s+Not Extended|511\s+Network Authentication Required)\b)",
    re.IGNORECASE,
)
_RESET_OR_HTTP2 = re.compile(
    r"(?:connection reset|reset by peer|connection aborted|broken pipe|"
    r"remote end closed|unexpected eof|http\s*/2\s+(?:stream|error|framing|protocol|reset|goaway)|"
    r"http2\s*:\s*(?:stream|error|framing|protocol|reset|goaway|server sent goaway|client connection lost)|"
    r"stream error:\s*stream ID \d+;\s*(?:INTERNAL_ERROR|REFUSED_STREAM);\s*received from peer|"
    r"curl:\s*\(92\))",
    re.IGNORECASE,
)


class ChecksumMismatchError(RuntimeError):
    """A downloaded or cached archive did not match its pinned digest."""


def retryable_transport_failure(
    stdout: str | bytes | None,
    stderr: str | bytes | None,
    *,
    error: BaseException | None = None,
) -> bool:
    """Recognize only transient transport failures; all other failures stop."""
    detail = "\n".join(filter(None, (output_text(stdout), output_text(stderr), str(error or ""))))
    if _INVALID_OR_CHECKSUM.search(detail):
        return False
    if _AUTH_ERROR.search(detail):
        return False
    if _THROTTLED.search(detail):
        return True
    if _SERVER_ERROR.search(detail) or _RESET_OR_HTTP2.search(detail):
        return True
    return False


def _forward(stream, payload: bytes | str | None) -> None:
    if not payload:
        return
    data = payload if isinstance(payload, bytes) else payload.encode("utf-8", errors="surrogatepass")
    binary = getattr(stream, "buffer", None)
    if binary is not None:
        binary.write(data)
        binary.flush()
    else:
        stream.write(output_text(data))
        stream.flush()


def run_dependency_command(
    command: list[str],
    *,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[bytes]:
    """Run one fetch/install command, preserving both streams from every try."""
    effective_env = None if env is None else dict(env)
    deadline_name = "DOBBYVPN_COMMAND_DEADLINE_MONOTONIC"
    if effective_env is not None and deadline_name not in effective_env and deadline_name in os.environ:
        effective_env[deadline_name] = os.environ[deadline_name]
    for attempt in range(1, MAX_ATTEMPTS + 1):
        timeout = command_deadline_remaining(effective_env)
        try:
            if timeout is None:
                result = subprocess.run(
                    command,
                    cwd=cwd,
                    env=effective_env,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    check=False,
                )
            else:
                result = run_finite_capture(
                    command,
                    cwd=cwd,
                    env=effective_env,
                    timeout_seconds=timeout,
                )
        except BaseException as error:
            stdout, stderr = exception_output(error)
            _forward(sys.stdout, stdout)
            _forward(sys.stderr, stderr)
            raise
        _forward(sys.stdout, result.stdout)
        _forward(sys.stderr, result.stderr)
        if result.returncode == 0:
            return result
        if attempt == MAX_ATTEMPTS or not retryable_transport_failure(result.stdout, result.stderr):
            return result
        delay = RETRY_DELAYS[attempt - 1]
        print(
            f"[dependency-fetch] transport failure; retrying attempt {attempt + 1}/{MAX_ATTEMPTS} "
            f"after {delay}s",
            file=sys.stderr,
            flush=True,
        )
        remaining = command_deadline_remaining(effective_env)
        time.sleep(delay if remaining is None else min(delay, remaining))
    raise AssertionError("dependency retry loop exhausted unexpectedly")


def _sha256(path: Path) -> str:
    with path.open("rb") as archive:
        return hashlib.file_digest(archive, "sha256").hexdigest()


def _set_response_timeout(response, timeout: float | None) -> None:
    if timeout is None:
        return
    raw = getattr(getattr(response, "fp", None), "raw", None)
    sock = getattr(raw, "_sock", None)
    if sock is not None:
        sock.settimeout(timeout)


def _download_once(url: str, destination: Path, timeout_for_blocking) -> None:
    request = Request(url, headers={"User-Agent": "DobbyVPN-dependency-fetch"})
    with urlopen(request, timeout=timeout_for_blocking()) as response, destination.open("wb") as output:
        while True:
            _set_response_timeout(response, timeout_for_blocking())
            chunk = response.read(1024 * 1024)
            if not chunk:
                break
            output.write(chunk)


def _download_retryable(error: BaseException, response_body: bytes) -> bool:
    body = output_text(response_body)
    if _INVALID_OR_CHECKSUM.search(body) or _AUTH_ERROR.search(body):
        return False
    if isinstance(error, HTTPError):
        if not retryable_transport_failure(None, response_body, error=error):
            return False
        return error.code == 429 or 500 <= error.code <= 599
    if isinstance(error, URLError):
        return retryable_transport_failure(None, None, error=error.reason)
    if isinstance(error, (ConnectionResetError, ConnectionAbortedError, BrokenPipeError)):
        return retryable_transport_failure(None, None, error=error)
    return False


def _download_with_retry(
    url: str,
    destination: Path,
    default_timeout: float | None,
    overall_timeout: float | None,
) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    inherited_deadline = command_deadline_remaining() is not None
    overall_deadline = None
    if not inherited_deadline and overall_timeout is not None:
        overall_deadline = time.monotonic() + overall_timeout

    def timeout_for_blocking() -> float | None:
        inherited = command_deadline_remaining()
        if inherited is not None:
            return inherited
        if overall_deadline is not None:
            remaining = overall_deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("dependency download allowance expired")
            return remaining
        return default_timeout

    for attempt in range(1, MAX_ATTEMPTS + 1):
        timeout_for_blocking()
        try:
            _download_once(url, destination, timeout_for_blocking)
            return
        except Exception as error:
            response_body = b""
            body_error: BaseException | None = None
            if isinstance(error, HTTPError):
                chunks: list[bytes] = []
                try:
                    while True:
                        _set_response_timeout(error, timeout_for_blocking())
                        chunk = error.read(64 * 1024)
                        if not chunk:
                            break
                        chunks.append(chunk)
                    response_body = b"".join(chunks)
                except BaseException as read_error:
                    response_body = b"".join(chunks)
                    body_error = read_error
            print(f"[dependency-fetch] attempt {attempt}/{MAX_ATTEMPTS} failed for {url}", file=sys.stderr)
            traceback.print_exception(error, file=sys.stderr)
            if body_error is not None:
                print("[dependency-fetch] also failed to read HTTP error body", file=sys.stderr)
                traceback.print_exception(body_error, file=sys.stderr)
            _forward(sys.stderr, response_body)
            if response_body and not response_body.endswith(b"\n"):
                _forward(sys.stderr, b"\n")
            try:
                destination.unlink(missing_ok=True)
            except BaseException as cleanup_error:
                print("[dependency-fetch] also failed to remove partial download", file=sys.stderr)
                traceback.print_exception(cleanup_error, file=sys.stderr)
                raise cleanup_error from error
            if body_error is not None:
                raise body_error from error
            if attempt == MAX_ATTEMPTS or not _download_retryable(error, response_body):
                raise
            delay = RETRY_DELAYS[attempt - 1]
            _forward(sys.stderr, f"[dependency-fetch] retrying after {delay}s\n")
            remaining = timeout_for_blocking()
            time.sleep(delay if remaining is None else min(delay, remaining))


def download_file(
    url: str,
    destination: Path,
    *,
    expected_sha256: str | None = None,
    default_timeout: float | None = 120,
    overall_timeout: float | None = None,
) -> None:
    """Fetch a file, using the archive cache only when its digest is pinned."""
    if expected_sha256 is not None and not re.fullmatch(r"[0-9a-f]{64}", expected_sha256):
        raise ValueError("expected SHA-256 must be 64 lowercase hexadecimal characters")
    cache_root_text = os.environ.get(TOOL_CACHE_ENV)
    cache_root = Path(cache_root_text).expanduser() if cache_root_text and expected_sha256 else None
    cache_path = cache_root / f"{expected_sha256}.archive" if cache_root else None
    destination.parent.mkdir(parents=True, exist_ok=True)

    if cache_path is not None and cache_path.exists():
        actual = _sha256(cache_path)
        if actual != expected_sha256:
            raise ChecksumMismatchError(
                f"cached archive SHA-256 mismatch: expected {expected_sha256}, got {actual} ({cache_path})"
            )
        temporary = destination.with_name(f".{destination.name}.{os.getpid()}.tmp")
        try:
            shutil.copyfile(cache_path, temporary)
            temporary.replace(destination)
        finally:
            temporary.unlink(missing_ok=True)
        return

    with tempfile.NamedTemporaryFile(
        prefix=f".{destination.name}.", suffix=".part", dir=destination.parent, delete=False,
    ) as temporary:
        part = Path(temporary.name)
    part.unlink()
    try:
        print(f"[dependency-fetch] downloading {url}", file=sys.stderr, flush=True)
        _download_with_retry(url, part, default_timeout, overall_timeout)
        if expected_sha256 is not None:
            actual = _sha256(part)
            if actual != expected_sha256:
                raise ChecksumMismatchError(
                    f"archive SHA-256 mismatch: expected {expected_sha256}, got {actual} ({url})"
                )
        if cache_path is not None:
            cache_root.mkdir(parents=True, exist_ok=True)
            cache_temporary = cache_path.with_name(f".{cache_path.name}.{os.getpid()}.tmp")
            try:
                shutil.copyfile(part, cache_temporary)
                cache_temporary.replace(cache_path)
            finally:
                cache_temporary.unlink(missing_ok=True)
        part.replace(destination)
    finally:
        part.unlink(missing_ok=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="operation", required=True)
    retry = commands.add_parser("retry", help="run one dependency download/install command")
    retry.add_argument("--cwd", type=Path)
    retry.add_argument("command", nargs=argparse.REMAINDER)
    download = commands.add_parser("download", help="download a pinned archive")
    download.add_argument("--url", required=True)
    download.add_argument("--output", type=Path, required=True)
    download.add_argument("--sha256", required=True)
    download.add_argument(
        "--timeout", type=float,
        help="per-request timeout; defaults to urllib's existing no-timeout behavior",
    )
    args = parser.parse_args()
    try:
        if args.operation == "retry":
            command = args.command[1:] if args.command[:1] == ["--"] else args.command
            if not command:
                parser.error("retry requires a command after --")
            result = run_dependency_command(command, cwd=args.cwd)
            return result.returncode
        download_file(
            args.url,
            args.output,
            expected_sha256=args.sha256,
            default_timeout=args.timeout,
        )
        return 0
    except (OSError, RuntimeError, subprocess.SubprocessError, URLError) as error:
        print(f"dependency fetch failed: {type(error).__name__}: {error}", file=sys.stderr)
        traceback.print_exception(error, file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError):
            return error.returncode
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
