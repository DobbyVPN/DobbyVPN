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
    # Android mini enters a real profile through the rendered Compose editor.
    # That cold UI path uses the same 60-second bound in functional.configure;
    # keep the configure operation consistent in every semantic scenario.
    _step("configure", "configure", 60),
    _step("connect", "connect", 40),
    _step("tunnel", "observe_tunnel"),
    # Android performs two sequential bounded identity probes during this
    # operation, in addition to the host/device routing handshake. Give that
    # shared observation enough time for both eight-second network bounds.
    _step("routing", "observe_routing_identity", 30),
)


# ``functional.network-transition`` remains a useful, directly selectable
# diagnostic scenario.  It is deliberately not part of either qualification
# suite while network-transition is deferred.  Keeping the definition here
# (rather than deleting it) preserves focused diagnostics without allowing a
# hosted limitation to masquerade as qualification coverage.
_QUALIFICATION_SCENARIO_IDS = (
    "functional.configure",
    "functional.core-connection",
    "functional.start-stop-start",
    "functional.product-process-loss",
)

SUITE_NAMES = ("mini", "full")

# The full lane is cumulative at the caller level: desktop callers add their
# native-window journeys to this shared functional lane.  The semantic
# scenario membership is intentionally the same as mini until deferred
# network-transition coverage is re-admitted by policy.
_SUITE_SCENARIO_IDS: dict[str, tuple[str, ...]] = {
    "mini": _QUALIFICATION_SCENARIO_IDS,
    "full": _QUALIFICATION_SCENARIO_IDS,
}


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
        max_duration_seconds=208,
    ),
    ScenarioDefinition(
        id="functional.start-stop-start",
        steps=_COMMON_CONNECT
        + (
            _step("disconnect", "disconnect", 10),
            _step("reconnect", "reconnect", 30),
            _step("second-tunnel", "observe_tunnel", 8),
            _step("second-routing", "observe_routing_identity", 30),
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
        max_duration_seconds=241,
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
        max_duration_seconds=193,
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
        max_duration_seconds=283,
    ),
)


def test_set() -> tuple[ScenarioDefinition, ...]:
    """Return every defined scenario, including diagnostic-only scenarios.

    Callers running qualification must use :func:`suite_set`; this complete
    definition list is retained so focused diagnostics can still resolve
    scenarios that are not currently in a qualification suite.
    """

    return TEST_SET


def suite_set(suite: str = "mini") -> tuple[ScenarioDefinition, ...]:
    """Return the canonical scenario membership for ``suite``.

    ``full`` is cumulative with respect to the caller's additional journeys
    (for example, native desktop-window actions); the shared semantic lane
    remains the mini membership while deferred scenarios are out of scope.
    """

    try:
        scenario_ids = _SUITE_SCENARIO_IDS[suite]
    except KeyError as error:
        raise ValueError(
            f"unknown suite {suite!r}; expected one of {', '.join(SUITE_NAMES)}"
        ) from error
    by_id = {scenario.id: scenario for scenario in TEST_SET}
    return tuple(by_id[scenario_id] for scenario_id in scenario_ids)


def select_scenarios(
    *,
    suite: str = "mini",
    scenario_ids: list[str] | tuple[str, ...] | None = None,
) -> tuple[ScenarioDefinition, ...]:
    """Resolve a suite or an explicit diagnostic scenario selection.

    Explicit selections intentionally resolve against all definitions, so a
    deferred scenario remains available for diagnostics.  Qualification
    callers must separately mark an explicit selection as incomplete.
    """

    suite_set(suite)  # validate the suite even when diagnostics are selected
    if not scenario_ids:
        return suite_set(suite)
    scenarios = tuple(get_scenario(value) for value in scenario_ids)
    if len({scenario.id for scenario in scenarios}) != len(scenarios):
        raise ValueError("scenario-id values must be unique")
    return scenarios


def validate_suite(
    suite: str,
    *,
    platform: str,
    entrypoint: str,
) -> None:
    """Validate caller/platform policy before any candidate setup.

    Hosted qualification is intentionally mini-only.  Local full is the
    additional real-window journey on Windows and macOS. Android and iOS full
    suites are future physical-device extensions, while Linux deliberately
    remains CLI/service mini-only. Raising before adapter construction
    prevents an unsupported request from being silently downgraded to mini or
    from doing installation/build work.
    """

    suite_set(suite)
    if suite == "mini":
        return
    if platform in {"android", "ios", "ios-simulator"}:
        raise ValueError(
            "FULL_SUITE_PHYSICAL_DEVICE_UNIMPLEMENTED: "
            f"{platform} does not implement the full physical-device suite"
        )
    if entrypoint == "hosted":
        raise ValueError(
            "FULL_SUITE_UNSUPPORTED_BY_HOSTED_ENTRYPOINT: hosted qualification "
            "accepts --suite mini"
        )
    if platform == "linux":
        raise ValueError(
            "FULL_SUITE_UNSUPPORTED_PLATFORM: Linux is CLI/service mini-only"
        )
    if platform not in {"windows", "macos"}:
        raise ValueError(f"FULL_SUITE_UNSUPPORTED_PLATFORM: {platform}")


def get_scenario(scenario_id: str) -> ScenarioDefinition:
    """Return one scenario or raise ``KeyError`` for an unknown id."""

    for scenario in TEST_SET:
        if scenario.id == scenario_id:
            return scenario
    raise KeyError(f"unknown scenario: {scenario_id!r}")
