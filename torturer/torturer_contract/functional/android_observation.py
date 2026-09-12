"""Safe observation contract for a future Android profile test seam."""

from __future__ import annotations

from dataclasses import dataclass
import math
import re
from typing import Mapping, Sequence

from .results import ConnectionIdentity


class AndroidObservationError(ValueError):
    """Raised when a candidate Android observation record is invalid or incomplete."""


@dataclass(frozen=True)
class AndroidProfileObservation:
    """Product observations used by the canonical functional assertions."""

    source_sha: str | None
    connections: tuple[ConnectionIdentity, ...]
    connection: ConnectionIdentity | None
    configured: bool
    connected: bool
    tunnel_interface: bool
    routing_verified: bool
    stability_verified: bool
    stability_sample_count: int
    stability_sample_interval_seconds: float
    network_transition_verified: bool
    process_loss_verified: bool
    latency_ms: float
    download_mbps: float
    upload_mbps: float
    disconnect_clean: bool
    restart_verified: bool
    reconnect_completed: bool
    second_tunnel_interface: bool
    second_routing_verified: bool
    final_disconnect_clean: bool
    cleanup_verified: bool
    error_code: str | None = None

    def __post_init__(self) -> None:
        if self.source_sha is not None and (
            not isinstance(self.source_sha, str)
            or re.fullmatch(r"[0-9a-f]{40}", self.source_sha) is None
        ):
            raise AndroidObservationError("source_sha is invalid")
        if (
            (not self.connections and self.error_code is None)
            or len(self.connections) != len(set(self.connections))
            or [item.index for item in self.connections] != list(range(len(self.connections)))
        ):
            raise AndroidObservationError("connections are invalid")
        if self.connection is not None and self.connection not in self.connections:
            raise AndroidObservationError("selected connection is invalid")
        for name in (
            "configured", "connected", "tunnel_interface",
            "routing_verified", "stability_verified", "disconnect_clean",
            "network_transition_verified", "process_loss_verified",
            "restart_verified", "reconnect_completed", "second_tunnel_interface",
            "second_routing_verified", "final_disconnect_clean",
            "cleanup_verified",
        ):
            if not isinstance(getattr(self, name), bool):
                raise AndroidObservationError(f"{name} must be boolean")
        for name in ("latency_ms", "download_mbps", "upload_mbps"):
            value = getattr(self, name)
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
                raise AndroidObservationError(f"{name} must be finite and non-negative")
        if (
            not isinstance(self.stability_sample_count, int)
            or isinstance(self.stability_sample_count, bool)
            or self.stability_sample_count <= 0
            or not isinstance(self.stability_sample_interval_seconds, (int, float))
            or isinstance(self.stability_sample_interval_seconds, bool)
            or not math.isfinite(float(self.stability_sample_interval_seconds))
            or self.stability_sample_interval_seconds <= 0
        ):
            raise AndroidObservationError("stability sampling is invalid")
    @classmethod
    def from_mapping(
        cls,
        value: Mapping[str, object],
        *,
        expected_source_sha: str | None = None,
    ) -> "AndroidProfileObservation":
        expected = {
            "configured", "connected",
            "connections",
            "tunnel_interface", "routing_verified",
            "stability_verified", "network_transition_verified",
            "stability_sample_count", "stability_sample_interval_seconds",
            "process_loss_verified", "latency_ms", "download_mbps",
            "upload_mbps", "disconnect_clean", "restart_verified", "reconnect_completed",
            "second_tunnel_interface", "second_routing_verified",
            "final_disconnect_clean", "cleanup_verified",
        }
        if expected_source_sha is not None:
            expected.add("source_sha")
        if not isinstance(value, Mapping) or not expected <= set(value):
            raise AndroidObservationError("observation has an unexpected shape")
        error_code = value.get("error_code")
        if error_code is not None and not isinstance(error_code, str):
            raise AndroidObservationError("error_code is invalid")
        raw_connections = value["connections"]
        if not isinstance(raw_connections, Sequence) or isinstance(raw_connections, (str, bytes)):
            raise AndroidObservationError("connections are invalid")
        def identity(raw: object) -> ConnectionIdentity:
            if not isinstance(raw, Mapping) or not {"index", "protocol"}.issubset(raw):
                raise ValueError
            return ConnectionIdentity(index=raw["index"], protocol=raw["protocol"])

        try:
            connections = tuple(
                identity(item) for item in raw_connections
            )
            raw_connection = value.get("connection")
            connection = None if raw_connection is None else identity(raw_connection)
        except (KeyError, TypeError, ValueError) as error:
            raise AndroidObservationError("connections are invalid") from error
        observation = cls(
            source_sha=value.get("source_sha"),
            connections=connections,
            connection=connection,
            configured=value["configured"],
            connected=value["connected"],
            tunnel_interface=value["tunnel_interface"],
            routing_verified=value["routing_verified"],
            stability_verified=value["stability_verified"],
            stability_sample_count=value["stability_sample_count"],
            stability_sample_interval_seconds=value["stability_sample_interval_seconds"],
            network_transition_verified=value["network_transition_verified"],
            process_loss_verified=value["process_loss_verified"],
            latency_ms=value["latency_ms"],
            download_mbps=value["download_mbps"],
            upload_mbps=value["upload_mbps"],
            disconnect_clean=value["disconnect_clean"],
            restart_verified=value["restart_verified"],
            reconnect_completed=value["reconnect_completed"],
            second_tunnel_interface=value["second_tunnel_interface"],
            second_routing_verified=value["second_routing_verified"],
            final_disconnect_clean=value["final_disconnect_clean"],
            cleanup_verified=value["cleanup_verified"],
            error_code=error_code,
        )
        if expected_source_sha is not None:
            if (
                re.fullmatch(r"[0-9a-f]{40}", expected_source_sha) is None
            ):
                raise AndroidObservationError("expected source_sha is invalid")
            if observation.source_sha != expected_source_sha:
                raise AndroidObservationError("source_sha does not match candidate")
        return observation

    def to_observations(self) -> dict[str, object]:
        """Map safe product facts to the canonical engine vocabulary."""

        if self.error_code is not None:
            raise AndroidObservationError("observation reports an error")
        return {
            "configured": self.configured,
            "connected": self.connected,
            "tunnel_interface": self.tunnel_interface,
            "routing_verified": self.routing_verified,
            "stability_verified": self.stability_verified,
            "stability_sample_count": self.stability_sample_count,
            "stability_sample_interval_seconds": self.stability_sample_interval_seconds,
            "network_transition_verified": self.network_transition_verified,
            "process_loss_verified": self.process_loss_verified,
            "latency_ms": self.latency_ms,
            "download_mbps": self.download_mbps,
            "upload_mbps": self.upload_mbps,
            "disconnect_clean": self.disconnect_clean,
            "restart_verified": self.restart_verified,
            "reconnect_completed": self.reconnect_completed,
            "second_tunnel_interface": self.second_tunnel_interface,
            "second_routing_verified": self.second_routing_verified,
            "final_disconnect_clean": self.final_disconnect_clean,
            "cleanup_verified": self.cleanup_verified,
        }
