"""Select one hosted platform adapter and its public test endpoints."""
from __future__ import annotations

from pathlib import Path
from typing import Any

from .android import AndroidHostedAdapter
from .cli import CommandRunner
from .linux import LinuxHostedAdapter
from .macos import MacOSHostedAdapter
from .windows import WindowsHostedAdapter

_ADAPTERS: dict[str, type] = {
    "linux": LinuxHostedAdapter,
    "windows": WindowsHostedAdapter,
    "macos": MacOSHostedAdapter,
    "android": AndroidHostedAdapter,
}

PUBLIC_IDENTITY_URL = "https://api.ipify.org"
PUBLIC_LATENCY_URL = "https://speed.cloudflare.com/__down?bytes=1"
PUBLIC_DOWNLOAD_URL = "https://speed.cloudflare.com/__down?bytes=1048576"
PUBLIC_UPLOAD_URL = "https://speed.cloudflare.com/__up"


def adapter_for_platform(
    platform: str,
    *,
    cli: Path | None = None,
    profile: Path,
    runner: CommandRunner,
    adb: Path | None = None,
    source_sha: str | None = None,
    local_mode: bool = False,
    service_pid: int | None = None,
    service_binary: Path | None = None,
    service_socket: Path | None = None,
    service_library_path: Path | None = None,
    service_pid_file: Path | None = None,
    service_identity_file: Path | None = None,
    service_log: Path | None = None,
    network_interface: str | None = None,
    routing_firewall_helper: Path | None = None,
    network_transition_helper: Path | None = None,
) -> Any:
    try:
        adapter_class = _ADAPTERS[platform]
    except KeyError as error:
        raise ValueError("unsupported hosted platform") from error
    kwargs: dict[str, object] = {
        "profile": profile,
        "runner": runner,
        "identity_url": PUBLIC_IDENTITY_URL,
        "download_url": PUBLIC_DOWNLOAD_URL,
        "upload_url": PUBLIC_UPLOAD_URL,
    }
    if platform == "android":
        if service_log is not None:
            raise ValueError("android adapter does not use service_log")
        for name, value in (
            ("cli", cli),
            ("service_pid", service_pid),
            ("service_binary", service_binary),
            ("service_socket", service_socket),
            ("service_library_path", service_library_path),
            ("service_pid_file", service_pid_file),
            ("service_identity_file", service_identity_file),
            ("network_interface", network_interface),
            ("routing_firewall_helper", routing_firewall_helper),
            ("network_transition_helper", network_transition_helper),
        ):
            if value is not None:
                raise ValueError(f"android adapter received unexpected {name}")
        kwargs.update({
            "adb": adb,
            "source_sha": source_sha,
            "latency_url": PUBLIC_LATENCY_URL,
        })
    else:
        if cli is None:
            raise ValueError("hosted desktop adapter requires --cli")
        kwargs["cli"] = cli
        kwargs["local_mode"] = local_mode
        if platform == "linux":
            if network_transition_helper is not None:
                raise ValueError("linux adapter received unexpected network_transition_helper")
            kwargs.update({
                "service_pid": service_pid,
                "service_binary": service_binary,
                "service_socket": service_socket,
                "service_library_path": service_library_path,
                "service_pid_file": service_pid_file,
                "service_identity_file": service_identity_file,
                "service_log": service_log,
                "network_interface": network_interface,
                "routing_firewall_helper": routing_firewall_helper,
            })
        elif platform in {"windows", "macos"}:
            if platform == "windows" and routing_firewall_helper is not None:
                raise ValueError(
                    f"{platform} adapter received unexpected routing_firewall_helper"
                )
            if service_log is not None:
                raise ValueError(f"{platform} adapter received unexpected service_log")
            if platform == "windows" and network_transition_helper is not None:
                raise ValueError("windows adapter received unexpected network_transition_helper")
            kwargs.update({
                "service_pid": service_pid,
                "service_binary": service_binary,
                "service_pid_file": service_pid_file,
                "service_identity_file": service_identity_file,
                "service_socket": service_socket,
            })
            if platform == "windows":
                kwargs["network_interface"] = network_interface
            else:
                kwargs["network_interface"] = network_interface
                kwargs["routing_firewall_helper"] = routing_firewall_helper
                kwargs["network_transition_helper"] = network_transition_helper
    return adapter_class(**kwargs)


__all__ = [
    "adapter_for_platform",
    "PUBLIC_DOWNLOAD_URL",
    "PUBLIC_IDENTITY_URL",
    "PUBLIC_LATENCY_URL",
    "PUBLIC_UPLOAD_URL",
]
