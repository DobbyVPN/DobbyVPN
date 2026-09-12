"""Platform-neutral functional scenarios.

Definitions contain semantic operations only. They intentionally do not carry
shell commands, profile values, endpoints, or platform-specific setup.
"""

from __future__ import annotations

from dataclasses import dataclass
import re

from .capabilities import Capability


_IDENTIFIER = re.compile(r"^[a-z][a-z0-9._-]*$")


@dataclass(frozen=True)
class ScenarioStep:
    """One semantic adapter operation in a scenario."""

    id: str
    operation: str
    timeout_seconds: int

    def __post_init__(self) -> None:
        if not _IDENTIFIER.fullmatch(self.id):
            raise ValueError(f"invalid scenario step id: {self.id!r}")
        if not _IDENTIFIER.fullmatch(self.operation):
            raise ValueError(f"invalid scenario operation: {self.operation!r}")
        if self.timeout_seconds <= 0:
            raise ValueError("scenario step timeout must be positive")

@dataclass(frozen=True)
class ScenarioDefinition:
    """A scenario and its canonical pass criteria."""

    id: str
    steps: tuple[ScenarioStep, ...]
    required_capabilities: frozenset[Capability]
    assertion_ids: tuple[str, ...]
    max_duration_seconds: int

    def __post_init__(self) -> None:
        if not _IDENTIFIER.fullmatch(self.id):
            raise ValueError(f"invalid scenario id: {self.id!r}")
        if not self.steps:
            raise ValueError("scenario must contain at least one step")
        step_ids = [step.id for step in self.steps]
        if len(step_ids) != len(set(step_ids)):
            raise ValueError(f"scenario has duplicate step ids: {self.id}")
        if not self.assertion_ids:
            raise ValueError("scenario must contain at least one assertion")
        if len(self.assertion_ids) != len(set(self.assertion_ids)):
            raise ValueError(f"scenario has duplicate assertions: {self.id}")
        if self.max_duration_seconds <= 0:
            raise ValueError("scenario maximum duration must be positive")
        if sum(step.timeout_seconds for step in self.steps) > self.max_duration_seconds:
            raise ValueError(f"scenario step bounds exceed scenario bound: {self.id}")

def _step(id: str, operation: str, timeout: int = 8) -> ScenarioStep:
    return ScenarioStep(id=id, operation=operation, timeout_seconds=timeout)


_COMMON_CONNECT = (
    _step("configure", "configure"),
    _step("connect", "connect", 40),
    _step("tunnel", "observe_tunnel"),
    _step("routing", "observe_routing_identity", 15),
)


TEST_SET: tuple[ScenarioDefinition, ...] = (
    ScenarioDefinition(
        id="functional.configure",
        # A software-emulated Android instrumentation process needs a larger
        # bounded cold-start and finalization allowance than native desktop
        # candidates. Faster platforms still return as soon as they finish.
        steps=(_step("configure", "configure", 60),),
        required_capabilities=frozenset({Capability.CONFIGURE}),
        assertion_ids=("configure.accepted",),
        max_duration_seconds=160,
    ),
    ScenarioDefinition(
        id="functional.core-connection",
        steps=_COMMON_CONNECT
        + (
            _step("stability", "measure_stability", 15),
            _step("throughput", "measure_throughput", 30),
            _step("disconnect", "disconnect", 10),
            _step("cleanup", "inspect_cleanup", 15),
        ),
        required_capabilities=frozenset(
            {
                Capability.CONFIGURE,
                Capability.CONNECT,
                Capability.TUNNEL_INTERFACE,
                Capability.ROUTING_IDENTITY,
                Capability.TRAFFIC_MEASUREMENT,
                Capability.DISCONNECT,
                Capability.RESOURCE_CLEANUP,
            }
        ),
        assertion_ids=(
            "configure.accepted",
            "tunnel.established",
            "routing.verified",
            "traffic.stable",
            "traffic.metrics_positive",
            "disconnect.clean",
            "cleanup.restored",
        ),
        max_duration_seconds=141,
    ),
    ScenarioDefinition(
        id="functional.start-stop-start",
        steps=_COMMON_CONNECT
        + (
            _step("disconnect", "disconnect", 10),
            _step("reconnect", "reconnect", 30),
            _step("second-tunnel", "observe_tunnel", 8),
            _step("second-routing", "observe_routing_identity", 15),
            _step("final-disconnect", "disconnect", 10),
            _step("cleanup", "inspect_cleanup", 15),
        ),
        required_capabilities=frozenset(
            {
                Capability.CONFIGURE,
                Capability.CONNECT,
                Capability.TUNNEL_INTERFACE,
                Capability.ROUTING_IDENTITY,
                Capability.DISCONNECT,
                Capability.RECONNECT,
                Capability.RESOURCE_CLEANUP,
            }
        ),
        assertion_ids=(
            "configure.accepted",
            "tunnel.established",
            "routing.verified",
            "disconnect.clean",
            "lifecycle.restart",
            "reconnect.completed",
            "tunnel.second_established",
            "routing.second_verified",
            "disconnect.final_clean",
            "cleanup.restored",
        ),
        max_duration_seconds=159,
    ),
    ScenarioDefinition(
        id="functional.network-transition",
        steps=_COMMON_CONNECT
        + (
            _step("network", "network_transition", 30),
            _step("disconnect", "disconnect", 10),
            _step("cleanup", "inspect_cleanup", 15),
        ),
        required_capabilities=frozenset(
            {
                Capability.CONFIGURE,
                Capability.CONNECT,
                Capability.TUNNEL_INTERFACE,
                Capability.ROUTING_IDENTITY,
                Capability.NETWORK_TRANSITION,
                Capability.DISCONNECT,
                Capability.RESOURCE_CLEANUP,
            }
        ),
        assertion_ids=(
            "configure.accepted",
            "tunnel.established",
            "routing.verified",
            "network.transition",
            "disconnect.clean",
            "cleanup.restored",
        ),
        max_duration_seconds=126,
    ),
    ScenarioDefinition(
        id="functional.product-process-loss",
        steps=_COMMON_CONNECT
        + (
            _step("process_loss", "process_loss", 120),
            _step("disconnect", "disconnect", 10),
            _step("cleanup", "inspect_cleanup", 15),
        ),
        required_capabilities=frozenset(
            {
                Capability.CONFIGURE,
                Capability.CONNECT,
                Capability.TUNNEL_INTERFACE,
                Capability.ROUTING_IDENTITY,
                Capability.PROCESS_LOSS,
                Capability.DISCONNECT,
                Capability.RESOURCE_CLEANUP,
            }
        ),
        assertion_ids=(
            "configure.accepted",
            "tunnel.established",
            "routing.verified",
            "process_loss.recovered",
            "disconnect.clean",
            "cleanup.restored",
        ),
        # Android proves real process absence between two bounded product
        # sessions. Reserve the shared adapter's finalization tail outside
        # that work without weakening the per-operation bounds.
        max_duration_seconds=276,
    ),
)


def test_set() -> tuple[ScenarioDefinition, ...]:
    """Return the immutable functional test set."""

    return TEST_SET


def get_scenario(scenario_id: str) -> ScenarioDefinition:
    """Return one scenario or raise ``KeyError`` for an unknown id."""

    for scenario in TEST_SET:
        if scenario.id == scenario_id:
            return scenario
    raise KeyError(f"unknown scenario: {scenario_id!r}")
