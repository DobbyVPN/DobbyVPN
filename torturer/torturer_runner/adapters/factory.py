"""Select the functional adapter for one supported platform."""

from __future__ import annotations

from pathlib import Path

from .android import AndroidAdapter
from .cli import CommandRunner
from .linux import LinuxAdapter
from .macos import MacOSAdapter
from .windows import WindowsAdapter

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
    service_pipe: str | None = None,
    service_socket: Path | str | None = None,
    service_library_path: Path | None = None,
    service_pid_file: Path | None = None,
    service_identity_file: Path | None = None,
    network_interface: str | None = None,
    routing_firewall_helper: Path | None = None,
    android_ui_mode: str = "protocol-matrix",
    app_apk: Path | None = None,
    test_companion_apk: Path | None = None,
) -> (
    LinuxAdapter
    | WindowsAdapter
    | MacOSAdapter
    | AndroidAdapter
):
    if platform == "android":
        for name, value in (
            ("cli", cli), ("service_pid", service_pid), ("service_binary", service_binary),
            ("service_pipe", service_pipe), ("service_socket", service_socket), ("service_library_path", service_library_path),
            ("service_pid_file", service_pid_file), ("service_identity_file", service_identity_file),
            ("network_interface", network_interface), ("routing_firewall_helper", routing_firewall_helper),
        ):
            if value is not None:
                raise ValueError(f"android adapter received unexpected {name}")
        common = dict(
            runner=runner,
            profile=profile,
            adb=adb,
            source_sha=source_sha,
            identity_url=PUBLIC_IDENTITY_URL,
            latency_url=PUBLIC_LATENCY_URL,
            download_url=PUBLIC_DOWNLOAD_URL,
            upload_url=PUBLIC_UPLOAD_URL,
            app_apk=app_apk,
            test_companion_apk=test_companion_apk,
        )
        if android_ui_mode not in {"gui-auto", "protocol-matrix"}:
            raise ValueError("unsupported Android adapter lane")
        return AndroidAdapter(**common, ui_mode=android_ui_mode)

    if platform not in {"linux", "windows", "macos"}:
        raise ValueError("unsupported platform")
    if android_ui_mode != "protocol-matrix":
        raise ValueError("android_ui_mode is only valid for Android")
    if app_apk is not None or test_companion_apk is not None:
        raise ValueError("Android APK paths are only valid for Android")
    if cli is None:
        raise ValueError("desktop adapter requires --cli")

    common = dict(
        cli=cli,
        profile=profile,
        runner=runner,
        identity_url=PUBLIC_IDENTITY_URL,
        download_url=PUBLIC_DOWNLOAD_URL,
        upload_url=PUBLIC_UPLOAD_URL,
        local_mode=local_mode,
        service_pid=service_pid,
        service_binary=service_binary,
        service_pid_file=service_pid_file,
        service_identity_file=service_identity_file,
        network_interface=network_interface,
    )
    if platform == "linux":
        return LinuxAdapter(
            **common,
            service_socket=service_socket,
            service_library_path=service_library_path,
            routing_firewall_helper=routing_firewall_helper,
        )
    if platform == "windows":
        if service_socket is not None or routing_firewall_helper is not None:
            raise ValueError("windows adapter received an unexpected platform helper")
        return WindowsAdapter(**common, service_pipe=service_pipe)
    if service_pipe is not None:
        raise ValueError("non-Windows adapter received unexpected service_pipe")
    return MacOSAdapter(
        **common,
        service_socket=service_socket,
        routing_firewall_helper=routing_firewall_helper,
    )


__all__ = [
    "adapter_for_platform",
    "PUBLIC_DOWNLOAD_URL",
    "PUBLIC_IDENTITY_URL",
    "PUBLIC_LATENCY_URL",
    "PUBLIC_UPLOAD_URL",
]
