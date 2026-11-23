"""Keep the bundled .NET runtime of the Windows MSI from going backwards.

Windows Installer decides component actions while it costs the new package, before a
major upgrade has removed the old product. A bundled runtime file whose version is lower
than the installed one is then skipped, and the later removal of the old product deletes it
(v1.5.15 upgraded over v1.5.11 lost 186 runtime files). Moving the removal does not help:
costing always comes first, and the v1.5.0 components share file paths with the current ones
under different component ids, so the old product has to be removed before files are installed.
The fix is to never ship an older runtime than a release already shipped, and to check it
here, on the exact MSI that is built.
"""

from __future__ import annotations

import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
FLOOR_FILE = ROOT / "ui" / "windows" / "installer" / "dotnet-runtime-floor.json"
RUNTIME_FILE = "coreclr.dll"

Version = tuple[int, int, int, int]


class RuntimeGuardError(ValueError):
    """The bundled runtime is older than one that was already shipped."""


def parse_version(text: str) -> Version:
    parts = str(text).strip().split(".")
    if len(parts) != 4 or not all(part.isdigit() for part in parts):
        raise RuntimeGuardError(f"not a four-part file version: {text!r}")
    return tuple(int(part) for part in parts)  # type: ignore[return-value]


def load_floor(path: Path = FLOOR_FILE) -> Version:
    try:
        record = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise RuntimeGuardError(f"runtime floor is unreadable: {path}") from error
    if not isinstance(record, dict) or record.get("file") != RUNTIME_FILE:
        raise RuntimeGuardError(f"runtime floor must describe {RUNTIME_FILE}")
    return parse_version(record.get("version", ""))


def check_runtime(candidate: str, *, floor: Version, previous: str | None = None) -> None:
    """Fail unless the candidate runtime is at least the floor and at least the previous release's."""
    actual = parse_version(candidate)
    if actual < floor:
        raise RuntimeGuardError(
            f"the MSI bundles {RUNTIME_FILE} {candidate}, older than the shipped floor "
            f"{'.'.join(map(str, floor))}; an upgrade over that release would lose runtime files"
        )
    if previous is not None and actual < parse_version(previous):
        raise RuntimeGuardError(
            f"the MSI bundles {RUNTIME_FILE} {candidate}, older than the previous release's {previous}; "
            "an upgrade over it would lose runtime files"
        )
