"""Qualification coverage validation shared by local and hosted runners."""

from __future__ import annotations

from collections import Counter
from typing import Any

from .results import ConnectionIdentity
from .scenarios import ScenarioDefinition, suite_set


def coverage_contract(
    connections: tuple[ConnectionIdentity, ...],
    selected_scenarios: tuple[ScenarioDefinition, ...],
    results: list[dict[str, Any]],
    *,
    suite: str = "mini",
    explicit_scenario_selection: bool = False,
) -> dict[str, object]:
    """Return whether results cover the complete selected protocol matrix.

    A qualification run must select the suite through its normal default path,
    execute every enabled scenario once for every discovered connection, and
    pass every resulting scenario. Focused selections remain useful for
    diagnostics but never qualify, even when the caller lists every scenario.
    """

    required_scenarios = suite_set(suite)
    required_ids = {scenario.id for scenario in required_scenarios}
    selected_ids = {scenario.id for scenario in selected_scenarios}
    connection_keys = {
        (connection.index, connection.protocol)
        for connection in connections
        if isinstance(connection, ConnectionIdentity)
    }
    connection_inventory_valid = (
        bool(connection_keys) and len(connection_keys) == len(connections)
    )
    expected = {
        (connection_index, protocol, scenario.id)
        for connection_index, protocol in connection_keys
        for scenario in selected_scenarios
    }

    observed: list[tuple[int, str, str]] = []
    invalid_results = 0
    actual_unavailable: set[tuple[str, str]] = set()
    passed = True
    for result in results:
        if not isinstance(result, dict):
            invalid_results += 1
            passed = False
            continue

        outcome = result.get("outcome")
        if outcome != "passed":
            passed = False
        if outcome == "unavailable":
            scenario = result.get("scenario")
            failure = result.get("failure")
            scenario_id = scenario.get("id") if isinstance(scenario, dict) else None
            reason_code = failure.get("code") if isinstance(failure, dict) else None
            actual_unavailable.add(
                (
                    scenario_id if isinstance(scenario_id, str) else "unknown",
                    reason_code if isinstance(reason_code, str) else "unknown",
                )
            )

        scenario = result.get("scenario")
        connection = result.get("connection")
        scenario_id = scenario.get("id") if isinstance(scenario, dict) else None
        connection_index = connection.get("index") if isinstance(connection, dict) else None
        protocol = connection.get("protocol") if isinstance(connection, dict) else None
        if (
            isinstance(scenario_id, str)
            and isinstance(connection_index, int)
            and not isinstance(connection_index, bool)
            and isinstance(protocol, str)
        ):
            observed.append((connection_index, protocol, scenario_id))
        else:
            invalid_results += 1
            passed = False

    counts = Counter(observed)
    observed_expected = set(counts) & expected
    missing = sorted(expected - observed_expected)
    duplicates = sorted(key for key, count in counts.items() if count > 1)
    unexpected = sorted(set(counts) - expected)
    matrix_complete = not missing and not duplicates and not unexpected and not invalid_results
    selection_complete = (
        selected_ids == required_ids
        and len(selected_scenarios) == len(required_scenarios)
        and not explicit_scenario_selection
    )
    all_results_passed = bool(results) and passed
    accepted = connection_inventory_valid and all_results_passed and not actual_unavailable
    complete = selection_complete and matrix_complete and accepted

    def render_keys(keys: list[tuple[int, str, str]]) -> list[dict[str, object]]:
        return [
            {"connection_index": index, "protocol": protocol, "scenario_id": scenario_id}
            for index, protocol, scenario_id in keys
        ]

    return {
        "suite": suite,
        "status": "complete" if complete else "coverage-contract-failed",
        "complete": complete,
        "test_set_scenario_count": len(required_ids),
        "expected_scenario_count": len(required_ids),
        "required_scenario_ids": sorted(required_ids),
        "connection_count": len(connections),
        "unique_connection_count": len(connection_keys),
        "connection_inventory_valid": connection_inventory_valid,
        "expected_result_count": len(expected),
        "selected_scenario_count": len(selected_ids),
        "selected_scenario_ids": sorted(selected_ids),
        "explicit_scenario_selection": explicit_scenario_selection,
        "result_count": len(results),
        "passed": all_results_passed,
        "selection_complete": selection_complete,
        "matrix_complete": matrix_complete,
        "missing_results": render_keys(missing),
        "duplicate_results": render_keys(duplicates),
        "unexpected_results": render_keys(unexpected),
        "invalid_result_count": invalid_results,
        "actual_unavailable": [
            {"scenario_id": scenario_id, "reason_code": reason}
            for scenario_id, reason in sorted(actual_unavailable)
        ],
    }


def qualification_exit_code(coverage: dict[str, object]) -> int:
    """Return success only for complete suite qualification."""

    return 0 if coverage.get("status") == "complete" else 2
