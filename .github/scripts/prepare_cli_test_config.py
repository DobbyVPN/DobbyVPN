#!/usr/bin/env python3
"""Prepare desktop CLI test configs for CI and local harness.

check-config uses the original config (URL, file path, or inline TOML file).
verify-session uses a local file with a dead first [[Outline]] profile prepended
so connectFirstWorkingProfile must skip the dead variant and connect via fallback.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path

DEAD_FIRST_OUTLINE = """\
[[Outline]]
Description = "CI dead first variant (TEST-NET-3)"
Server = "203.0.113.1"
Port = 1
Method = "chacha20-ietf-poly1305"
Password = "dead-ci-fallback-probe-invalid-key"

"""


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def is_url(config: str) -> bool:
    return config.startswith("http://") or config.startswith("https://")


def download_config(url: str) -> str:
    try:
        with urllib.request.urlopen(url, timeout=60) as response:
            return response.read().decode("utf-8")
    except urllib.error.URLError as exc:
        fail(f"failed to download config from {url}: {exc}")


def load_config_body(config: str) -> tuple[str, str]:
    """Return (check_config_arg, config_body)."""
    if is_url(config):
        return config, download_config(config)

    path = Path(config)
    if path.is_file():
        return str(path.resolve()), path.read_text(encoding="utf-8")

    inline_path = Path("cli-test-config.toml")
    inline_path.write_text(config, encoding="utf-8")
    return str(inline_path.resolve()), config


def write_fallback_config(config_body: str, fallback_out: Path) -> Path:
    fallback_out.write_text(DEAD_FIRST_OUTLINE + config_body.lstrip("\ufeff"), encoding="utf-8")
    return fallback_out.resolve()


def resolve_paths(config: str, fallback_out: Path | None) -> tuple[str, str]:
    check_config_arg, config_body = load_config_body(config)
    fallback_path = fallback_out or Path("cli-test-config-fallback.toml")
    verify_session_arg = str(write_fallback_config(config_body, fallback_path))
    return check_config_arg, verify_session_arg


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--config",
        default=os.environ.get("DOBBYVPN_CLI_TEST_CONFIG", ""),
        help="Config URL, file path, or inline TOML (default: DOBBYVPN_CLI_TEST_CONFIG).",
    )
    parser.add_argument(
        "--fallback-out",
        type=Path,
        default=Path("cli-test-config-fallback.toml"),
        help="Path for verify-session config with dead first Outline profile.",
    )
    parser.add_argument(
        "--format",
        choices=("lines", "json"),
        default="lines",
        help="Output format: two stdout lines (check, verify) or JSON object.",
    )
    args = parser.parse_args()

    if not args.config.strip():
        fail("config is empty; pass --config or set DOBBYVPN_CLI_TEST_CONFIG")

    check_config_arg, verify_session_arg = resolve_paths(args.config.strip(), args.fallback_out)

    if args.format == "json":
        print(
            json.dumps(
                {
                    "check_config_arg": check_config_arg,
                    "verify_session_arg": verify_session_arg,
                }
            )
        )
        return

    print(check_config_arg)
    print(verify_session_arg)


if __name__ == "__main__":
    main()
