"""Expected unavailable scenarios on disposable hosted runners."""

from __future__ import annotations


PUBLIC_EXPECTED_UNAVAILABLE: dict[str, frozenset[tuple[str, str]]] = {
    "linux": frozenset({
        ("functional.network-transition", "HOSTED_LINUX_INTERFACE_REQUIRED"),
    }),
    "windows": frozenset({
        ("functional.network-transition", "HOSTED_WINDOWS_UPLINK_TOGGLE_UNSUPPORTED"),
    }),
    "macos": frozenset({
        ("functional.network-transition", "HOSTED_MACOS_UPLINK_TOGGLE_UNSUPPORTED"),
    }),
    "android": frozenset(),
}

__all__ = [
    "PUBLIC_EXPECTED_UNAVAILABLE",
]
