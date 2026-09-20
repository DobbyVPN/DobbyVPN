"""Trusted-runner adapters for the canonical functional engine.

The desktop adapter drives the product's public CLI, with an optional
production Fyne UI wrapper for Windows/macOS GUI qualification; Android
uses the rendered Go/Fyne activity together with the native VPN-service
adapter for its hosted UI and functional lanes.
Scenario meaning and assertions stay in ``torturer_contract.functional``;
provider credentials and profiles are supplied by a separate trusted workflow
boundary.
"""

from .cli import CommandResult, HostedAdapterError, HostedCLIAdapter, SubprocessRunner
from .factory import adapter_for_platform

__all__ = [
    "CommandResult",
    "HostedAdapterError",
    "HostedCLIAdapter",
    "SubprocessRunner",
    "adapter_for_platform",
]
