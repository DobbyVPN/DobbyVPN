"""Canonical cross-platform functional-test contract.

This package contains only public scenario semantics and safe result
metadata. Platform commands, credentials, and evidence storage belong to
the adapters and callers that execute it.
"""

from .android_observation import (
    AndroidObservationError,
    AndroidProfileObservation,
)
from .assertions import (
    AssertionOutcome,
    evaluate_assertion,
    evaluate_assertions,
)
from .capabilities import Capability
from .engine import (
    CapabilityUnavailable,
    FunctionalEngine,
    ScenarioAdapter,
    ScenarioExecutionError,
)
from .results import (
    ConnectionIdentity,
    RunProvenance,
    ScenarioResult,
)
from .scenarios import (
    ScenarioDefinition,
    ScenarioStep,
    get_scenario,
    test_set,
)

__all__ = [
    "AssertionOutcome",
    "AndroidObservationError",
    "AndroidProfileObservation",
    "CapabilityUnavailable",
    "Capability",
    "ConnectionIdentity",
    "FunctionalEngine",
    "RunProvenance",
    "ScenarioAdapter",
    "ScenarioDefinition",
    "ScenarioExecutionError",
    "ScenarioResult",
    "ScenarioStep",
    "evaluate_assertion",
    "evaluate_assertions",
    "get_scenario",
    "test_set",
]
