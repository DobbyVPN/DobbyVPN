"""Typed capabilities understood by the canonical functional engine."""

from __future__ import annotations

from enum import Enum


class Capability(str, Enum):
    """An adapter feature required by one or more canonical scenarios."""

    CONFIGURE = "configure"
    CONNECT = "connect"
    TUNNEL_INTERFACE = "tunnel_interface"
    ROUTING_IDENTITY = "routing_identity"
    TRAFFIC_MEASUREMENT = "traffic_measurement"
    DISCONNECT = "disconnect"
    RESOURCE_CLEANUP = "resource_cleanup"
    RECONNECT = "reconnect"
    NETWORK_TRANSITION = "network_transition"
    PROCESS_LOSS = "process_loss"


def capability_values(capabilities: frozenset[Capability] | set[Capability]) -> frozenset[str]:
    """Return the stable wire representation of adapter capabilities."""

    return frozenset(capability.value for capability in capabilities)
