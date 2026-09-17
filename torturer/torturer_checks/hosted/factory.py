"""Select one hosted platform adapter and its public test endpoints."""
from __future__ import annotations

from pathlib import Path

from .android import AndroidHostedAdapter
from .cli import CommandRunner
from .linux import LinuxHostedAdapter
from .macos import MacOSHostedAdapter
from .windows import WindowsHostedAdapter
from .ui import HeadlessUIAdapter

PUBLIC_IDENTITY_URL = "https://api.ipify.org"
PUBLIC_LATENCY_URL = "https://speed.cloudflare.com/__down?bytes=1"
PUBLIC_DOWNLOAD_URL = "https://speed.cloudflare.com/__down?bytes=1048576"
PUBLIC_UPLOAD_URL = "https://speed.cloudflare.com/__up"


def adapter_for_platform(
    platform: str,
    *,
    cli: Path | None = None,
    ui_test: Path | None = None,
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
) -> (
    LinuxHostedAdapter
    | WindowsHostedAdapter
    | MacOSHostedAdapter
    | AndroidHostedAdapter
):
    if platform == "android":
        if service_log is not None:
            raise ValueError("android adapter does not use service_log")
        for name, value in (
            ("cli", cli),
            ("ui_test", ui_test),
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
        return AndroidHostedAdapter(
            runner=runner,
            profile=profile,
            adb=adb,
            source_sha=source_sha,
            identity_url=PUBLIC_IDENTITY_URL,
            latency_url=PUBLIC_LATENCY_URL,
            download_url=PUBLIC_DOWNLOAD_URL,
            upload_url=PUBLIC_UPLOAD_URL,
        )

    if platform not in {"linux", "windows", "macos"}:
        raise ValueError("unsupported hosted platform")
    if cli is None:
        raise ValueError("hosted desktop adapter requires --cli")

    if platform == "linux":
        if network_transition_helper is not None:
            raise ValueError("linux adapter received unexpected network_transition_helper")
        adapter = LinuxHostedAdapter(
            cli=cli,
            profile=profile,
            runner=runner,
            identity_url=PUBLIC_IDENTITY_URL,
            download_url=PUBLIC_DOWNLOAD_URL,
            upload_url=PUBLIC_UPLOAD_URL,
            local_mode=local_mode,
            service_pid=service_pid,
            service_binary=service_binary,
            service_socket=service_socket,
            service_library_path=service_library_path,
            service_pid_file=service_pid_file,
            service_identity_file=service_identity_file,
            service_log=service_log,
            network_interface=network_interface,
            routing_firewall_helper=routing_firewall_helper,
        )
        return _wrap_ui(adapter, ui_test=ui_test, profile=profile, runner=runner)
    if platform == "windows":
        if routing_firewall_helper is not None:
            raise ValueError("windows adapter received unexpected routing_firewall_helper")
        if service_log is not None:
            raise ValueError("windows adapter received unexpected service_log")
        if network_transition_helper is not None:
            raise ValueError("windows adapter received unexpected network_transition_helper")
        adapter = WindowsHostedAdapter(
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
            service_socket=service_socket,
            network_interface=network_interface,
        )
        return _wrap_ui(adapter, ui_test=ui_test, profile=profile, runner=runner)

    if service_log is not None:
        raise ValueError("macos adapter received unexpected service_log")
    adapter = MacOSHostedAdapter(
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
        service_socket=service_socket,
        network_interface=network_interface,
        routing_firewall_helper=routing_firewall_helper,
        network_transition_helper=network_transition_helper,
    )
    return _wrap_ui(adapter, ui_test=ui_test, profile=profile, runner=runner)


def _wrap_ui(adapter, *, ui_test: Path | None, profile: Path, runner: CommandRunner):
    if ui_test is None:
        return adapter
    return HeadlessUIAdapter(
        base=adapter,
        ui_test=ui_test,
        profile=profile,
        runner=runner,
    )


__all__ = [
    "adapter_for_platform",
    "PUBLIC_DOWNLOAD_URL",
    "PUBLIC_IDENTITY_URL",
    "PUBLIC_LATENCY_URL",
    "PUBLIC_UPLOAD_URL",
]
