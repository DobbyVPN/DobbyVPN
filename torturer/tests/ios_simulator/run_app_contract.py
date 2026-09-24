"""Run the hosted iOS Simulator SwiftUI app check without credentials."""

from __future__ import annotations

import argparse
from pathlib import Path
import sys


if __package__ in {None, ""}:  # pragma: no cover - exercised by the local launcher
    sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from torturer_checks.ios_simulator_app import (  # noqa: E402
    IOSSimulatorAppContractError,
    RunBudget,
    SubprocessCommandRunner,
    public_ios_simulator_app_contract,
    prepare_ios_simulator_candidate,
    run_ios_simulator_app_contract,
)


def parse_arguments(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate-root", type=Path, required=True)
    parser.add_argument("--work-dir", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_arguments(argv)
    try:
        # The Simulator target checks the shared SwiftUI frontend without
        # embedding the physical-device packet-tunnel extension.
        contract = public_ios_simulator_app_contract("arm64")
        runner = SubprocessCommandRunner()
        budget = RunBudget()
        prepare_ios_simulator_candidate(
            candidate_root=args.candidate_root,
            work_dir=args.work_dir,
            runner=runner,
            contract=contract,
            budget=budget,
        )
        evidence = run_ios_simulator_app_contract(
            candidate_root=args.candidate_root,
            work_dir=args.work_dir,
            runner=runner,
            contract=contract,
            budget=budget,
        )
    except IOSSimulatorAppContractError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    print(
        "iOS-Simulator SwiftUI XCTest UI mini contract passed: "
        f"{evidence.simulator.name} ({evidence.simulator.runtime}); "
        "VPN/NetworkExtension success is not asserted"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
