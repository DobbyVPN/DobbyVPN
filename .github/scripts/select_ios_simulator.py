#!/usr/bin/env python3
"""Select one available iPhone Simulator deterministically from simctl JSON."""
from __future__ import annotations

import json
import re
import sys
from typing import Any


PREFERRED_MODELS = ("iPhone 16 Pro", "iPhone 16")
RUNTIME_VERSION = re.compile(r"iOS-(\d+(?:-\d+)*)$")


def runtime_version(runtime: str) -> tuple[int, ...] | None:
    match = RUNTIME_VERSION.search(runtime)
    if match is None:
        return None
    return tuple(int(part) for part in match.group(1).split("-"))


def select_simulator(devices: dict[str, Any]) -> str:
    candidates: list[tuple[tuple[int, ...], int, str, str]] = []
    for runtime, entries in devices.items():
        version = runtime_version(runtime)
        if version is None or not isinstance(entries, list):
            continue
        for entry in entries:
            if not isinstance(entry, dict) or not entry.get("isAvailable", False):
                continue
            name = entry.get("name")
            udid = entry.get("udid")
            if not isinstance(name, str) or not isinstance(udid, str) or not name.startswith("iPhone"):
                continue
            try:
                model_rank = PREFERRED_MODELS.index(name)
            except ValueError:
                model_rank = len(PREFERRED_MODELS)
            candidates.append((version, model_rank, name, udid))

    if not candidates:
        raise ValueError("no available iPhone Simulator was found")

    # Prefer the newest installed iOS runtime, then an explicitly preferred
    # model, then stable lexical ordering. This avoids dependence on simctl's
    # object order or on whatever device another job happened to boot first.
    latest_version = max(candidate[0] for candidate in candidates)
    current_runtime = [candidate for candidate in candidates if candidate[0] == latest_version]
    return min(current_runtime, key=lambda candidate: candidate[1:])[3]


def main() -> int:
    payload = json.load(sys.stdin)
    devices = payload.get("devices") if isinstance(payload, dict) else None
    if not isinstance(devices, dict):
        raise ValueError("simctl JSON is missing a devices object")
    print(select_simulator(devices))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ValueError, json.JSONDecodeError) as error:
        print(f"error: {error}", file=sys.stderr)
        raise SystemExit(1)
