"""Run the shared desktop functional lane against an installed package.

The local VM and hosted runner own installation, service startup, and cleanup.
This entry point only translates their installed-package descriptor and
runtime details into the existing local or hosted functional adapter.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
from typing import Any


PLATFORMS = ("linux", "windows", "macos")
MODES = ("local", "hosted")
SHA256 = re.compile(r"^[0-9a-f]{64}$")


class DesktopPlatformError(ValueError):
    """An invalid package descriptor or functional lane request."""


def _document(path: Path, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, ValueError) as error:
        raise DesktopPlatformError(f"{label} is unreadable: {path}") from error
    if not isinstance(value, dict):
        raise DesktopPlatformError(f"{label} must contain a JSON object")
    return value


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="action", required=True)
    test = subcommands.add_parser("test", help="run the shared desktop functional mini suite")
    test.add_argument("--mode", choices=MODES, required=True)
    test.add_argument("--platform", choices=PLATFORMS, required=True)
    test.add_argument("--installed-descriptor", type=Path, required=True)
    test.add_argument("--profile", type=Path, required=True)
    test.add_argument("--suite", choices=("mini",), default="mini")
    test.add_argument("--output", type=Path, required=True)
    test.add_argument("--raw-log-dir", type=Path, required=True)
    test.add_argument("--platform-version", required=True)
    test.add_argument("--lane-timeout-seconds", type=float, required=True)
    test.add_argument("--service-pid", type=int, required=True)
    test.add_argument("--service-pipe")
    test.add_argument("--service-socket", type=Path)
    test.add_argument("--service-pid-file", type=Path)
    test.add_argument("--service-identity-file", type=Path)
    test.add_argument("--network-interface")
    test.add_argument("--routing-firewall-helper", type=Path)
    return parser


def _functional_arguments(args: argparse.Namespace, installed: dict[str, Any]) -> list[str]:
    if installed.get("schema") != 1 or installed.get("mode") != "installed-package":
        raise DesktopPlatformError("installed package descriptor schema or mode is invalid")
    if installed.get("platform") != args.platform:
        raise DesktopPlatformError("installed package descriptor platform does not match")
    package_path = installed.get("package_path")
    package_sha256 = installed.get("package_sha256")
    if not isinstance(package_path, str) or not isinstance(package_sha256, str):
        raise DesktopPlatformError("installed package descriptor has no package identity")
    if SHA256.fullmatch(package_sha256) is None:
        raise DesktopPlatformError("installed package descriptor package hash is invalid")
    package = Path(package_path)
    try:
        with package.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()
    except OSError as error:
        raise DesktopPlatformError("tested package is unavailable") from error
    if digest != package_sha256:
        raise DesktopPlatformError("tested package hash differs from the installed package descriptor")
    if installed.get("installed") is not True:
        raise DesktopPlatformError("installed package descriptor does not report a successful install")
    for name in ("cli", "service", "network"):
        if not isinstance(installed.get(name), str):
            raise DesktopPlatformError(f"installed package descriptor is missing {name}")
    cli = Path(installed["cli"])
    service = Path(installed["service"])
    if not cli.is_file() or not service.is_file():
        raise DesktopPlatformError("installed package CLI or service is unavailable")

    if args.platform == "windows":
        endpoint = args.service_pipe or "DobbyVPN.Control"
        if args.service_socket is not None:
            raise DesktopPlatformError("Windows functional lane cannot use a Unix socket")
        endpoint_args = ["--service-pipe", endpoint]
    else:
        endpoint = args.service_socket or (
            Path("/var/run/dobbyvpn/control.sock")
            if args.platform == "macos"
            else Path(installed["network"])
        )
        if args.service_pipe is not None:
            raise DesktopPlatformError("Unix functional lane cannot use a Windows pipe")
        endpoint_args = ["--service-socket", str(endpoint)]

    argv = [
        "--platform", args.platform,
        "--cli", str(cli),
        "--profile", str(args.profile),
        "--output", str(args.output),
        "--raw-log-dir", str(args.raw_log_dir),
        "--platform-version", args.platform_version,
        "--lane-timeout-seconds", str(args.lane_timeout_seconds),
        "--suite", args.suite,
        "--service-pid", str(args.service_pid),
        "--service-binary", str(service),
        *endpoint_args,
    ]
    library_path = installed.get("library_path")
    if isinstance(library_path, str):
        argv.extend(("--service-library-path", library_path))
    if args.service_pid_file is not None:
        if args.mode == "local":
            argv.extend(("--service-pid-file", str(args.service_pid_file)))
    if args.service_identity_file is not None:
        if args.mode == "local":
            argv.extend(("--service-identity-file", str(args.service_identity_file)))
    if args.network_interface is not None:
        argv.extend(("--network-interface", args.network_interface))
    helper_root = Path(__file__).resolve().parents[1] / "helpers" / "local"
    routing_helper = args.routing_firewall_helper
    if args.platform == "linux" and routing_helper is None:
        routing_helper = helper_root / "linux" / "routing-probe-firewall"
    if args.platform == "macos" and routing_helper is None:
        routing_helper = helper_root / "macos" / "routing-firewall"
    if routing_helper is not None:
        argv.extend(("--routing-firewall-helper", str(routing_helper)))
    source_sha = installed.get("source_sha")
    if isinstance(source_sha, str) and source_sha:
        argv.extend(("--source-sha", source_sha))
    return argv


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    installed = _document(args.installed_descriptor, "installed package descriptor")
    functional_argv = _functional_arguments(args, installed)
    print(
        "desktop package under test: "
        f"{installed['package_path']} sha256={installed['package_sha256']}",
        file=sys.stderr,
        flush=True,
    )
    if args.mode == "local":
        from . import functional

        return functional.main(functional_argv)

    from .hosted import run as hosted_run

    return hosted_run.main(functional_argv)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except DesktopPlatformError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(2)
