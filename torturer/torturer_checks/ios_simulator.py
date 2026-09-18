"""Small shell-free command builders for the local iOS Simulator check."""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import re


_UDID = re.compile(r"[0-9A-Fa-f]{8}-(?:[0-9A-Fa-f]{4}-){3}[0-9A-Fa-f]{12}\Z")
_BUNDLE_ID = re.compile(r"[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\Z")


class IOSSimulatorContractError(ValueError):
    """A Simulator command argument is outside the supported shape."""


@dataclass(frozen=True)
class SimulatorApp:
    """The app path and fixed build labels, without an artifact attestation."""

    app_path: Path
    bundle_identifier: str
    architecture: str


def simctl_boot_command(device_udid: str) -> list[str]:
    return ["xcrun", "simctl", "boot", _validate_udid(device_udid)]


def simctl_bootstatus_command(device_udid: str) -> list[str]:
    return ["xcrun", "simctl", "bootstatus", _validate_udid(device_udid), "-b"]


def simctl_install_command(device_udid: str, app: str | Path) -> list[str]:
    app_path = Path(app)
    if app_path.suffix != ".app":
        raise IOSSimulatorContractError("Simulator install path must end in .app")
    return ["xcrun", "simctl", "install", _validate_udid(device_udid), str(app_path)]


def simctl_launch_command(
    device_udid: str,
    bundle_identifier: str,
    *,
    console: bool = False,
) -> list[str]:
    _validate_bundle_identifier(bundle_identifier)
    command = ["xcrun", "simctl", "launch"]
    if console:
        command.append("--console")
    command.extend(("--terminate-running-process", _validate_udid(device_udid), bundle_identifier))
    return command


def simctl_terminate_command(device_udid: str, bundle_identifier: str) -> list[str]:
    _validate_bundle_identifier(bundle_identifier)
    return ["xcrun", "simctl", "terminate", _validate_udid(device_udid), bundle_identifier]


def _validate_bundle_identifier(value: str) -> None:
    if not isinstance(value, str) or not _BUNDLE_ID.fullmatch(value):
        raise IOSSimulatorContractError("bundle identifier has an unsupported format")


def _validate_udid(value: str) -> str:
    if not isinstance(value, str) or not _UDID.fullmatch(value):
        raise IOSSimulatorContractError("Simulator UDID has an unsupported format")
    return value.upper()
