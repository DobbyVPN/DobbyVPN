"""Platform adapters for the canonical functional engine.

Scenario meaning and assertions stay in ``torturer_contract.functional``;
provider credentials and profiles are supplied by the trusted workflow.
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
