"""Parser for DobbyVPN's public machine-readable CLI status."""

from __future__ import annotations

from dataclasses import dataclass
import json


class CLIStatusError(ValueError):
    """The CLI returned a value outside the public status contract."""


@dataclass(frozen=True)
class PublicCLIStatus:
    code: int
    state: str


_CODE_TO_STATE = {
    0: "Disconnected",
    1: "Connecting",
    2: "Connected",
}


def parse_public_status(output: str) -> PublicCLIStatus:
    """Parse and validate one complete ``status --json`` response.

    Formatting whitespace and extra fields are insignificant. The integer
    ``code`` and ``state`` label must be a known pair.
    """

    try:
        value = json.loads(output)
    except (TypeError, ValueError) as error:
        raise CLIStatusError("status output is not valid JSON") from error
    if type(value) is not dict:
        raise CLIStatusError("status JSON must be an object")
    code = value.get("code")
    state = value.get("state")
    if type(code) is not int:
        raise CLIStatusError("status code must be an integer")
    if type(state) is not str:
        raise CLIStatusError("status state must be a string")
    expected_state = _CODE_TO_STATE.get(code)
    if expected_state is None:
        raise CLIStatusError("status code is unknown")
    if state != expected_state:
        raise CLIStatusError("status code and state do not match")
    return PublicCLIStatus(code=code, state=state)


__all__ = ["CLIStatusError", "PublicCLIStatus", "parse_public_status"]
