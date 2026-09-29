"""Platform adapters for the canonical functional engine.

Scenario meaning and assertions stay in ``torturer_contract``;
provider credentials and profiles are supplied by the caller.
"""

from .cli import CommandResult, AdapterError, CLIAdapter, SubprocessRunner
from .factory import adapter_for_platform

__all__ = [
    "CommandResult",
    "AdapterError",
    "CLIAdapter",
    "SubprocessRunner",
    "adapter_for_platform",
]
