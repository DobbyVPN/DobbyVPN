"""Expose existing platform adapter observations to one native desktop case."""

from __future__ import annotations

import math
import time
from typing import Any, Callable

from torturer_contract.scenarios import ScenarioStep


_OBSERVATIONS = (
    ("observe-tunnel", "observe_tunnel"),
    ("observe-routing", "observe_routing_identity"),
    ("measure-stability", "measure_stability"),
    ("measure-throughput", "measure_throughput"),
)
_THROUGHPUT_FIELDS = ("latency_ms", "download_mbps", "upload_mbps")


def for_base_adapter(base: Any) -> Callable[[dict[str, Any]], dict[str, object]]:
    """Build the small request callback while retaining this adapter instance."""

    def observe(request: dict[str, Any]) -> dict[str, object]:
        if not isinstance(request, dict):
            raise ValueError("native observation request must be an object")
        operation = request.get("operation")
        timeout = request.get("timeout_seconds")
        if (not isinstance(operation, str) or isinstance(timeout, bool)
                or not isinstance(timeout, (int, float)) or not math.isfinite(timeout)
                or timeout <= 0):
            raise ValueError("native observation request has an invalid operation or timeout")

        if operation == "snapshot":
            result = base._snapshot(float(timeout), "NATIVE_OBSERVATION_SNAPSHOT_FAILED")
            return _mapping_result(operation, result)
        if operation == "prepare-connect":
            base.prepare_native_connect(float(timeout))
            return {"prepared": True}
        if operation == "observe":
            return _run_observations(base, float(timeout))
        if operation == "cleanup":
            deadline = time.monotonic() + float(timeout)
            connected = base._connected(_remaining(deadline))
            clean = base._cleanup_verified(_remaining(deadline))
            return {"connected": connected, "cleanup_verified": clean}
        if operation == "restart-service":
            result = base.restart_service_for_native_ui(float(timeout))
            return _mapping_result(operation, result)
        if operation == "reset":
            base.reset(timeout_seconds=float(timeout))
            return {"reset": True}
        raise ValueError(f"unsupported native observation operation: {operation}")

    return observe


def _run_observations(base: Any, timeout: float) -> dict[str, object]:
    deadline = time.monotonic() + timeout
    observations: dict[str, object] = {}
    for identifier, operation in _OBSERVATIONS:
        remaining = deadline - time.monotonic()
        step_timeout = math.floor(remaining)
        if step_timeout < 1:
            raise TimeoutError("native observation request exhausted its timeout")
        result = base.execute(ScenarioStep(identifier, operation, step_timeout))
        observations.update(_mapping_result(operation, result))
    observations["throughput_positive"] = _throughput_positive(observations)
    for field in _THROUGHPUT_FIELDS:
        value = observations.get(field)
        if isinstance(value, float) and not math.isfinite(value):
            # JSON has no numeric NaN or infinity values. Preserve the diagnostic
            # spelling while keeping the native-helper response valid JSON.
            observations[field] = "NaN" if math.isnan(value) else ("Infinity" if value > 0 else "-Infinity")
    return observations


def _throughput_positive(observations: dict[str, object]) -> bool:
    for field in _THROUGHPUT_FIELDS:
        value = observations.get(field)
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            return False
        try:
            number = float(value)
        except OverflowError:
            return False
        if not math.isfinite(number) or number <= 0:
            return False
    return True


def _mapping_result(operation: str, result: object) -> dict[str, object]:
    if not isinstance(result, dict):
        raise TypeError(f"base adapter {operation} returned a non-object result")
    if any(not isinstance(key, str) for key in result):
        raise TypeError(f"base adapter {operation} returned a non-string key")
    return result


def _remaining(deadline: float) -> float:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("native cleanup observation exhausted its timeout")
    return remaining
