"""Secretless Simulator lane using the existing app lifecycle checks."""
from __future__ import annotations

import json
from pathlib import Path
import platform

from . import ios_simulator_app as ios


def run(
    run_dir: Path,
    logs: Path,
    timeout: float,
    architecture: str | None,
    simulator_mode: str,
) -> dict:
    from .local_vm import _read_state, _write_json

    if simulator_mode not in ios.SIMULATOR_MODES:
        raise ValueError("ios-simulator requires mode mini or metal")

    contract = ios.public_ios_simulator_app_contract(
        architecture or ("amd64" if platform.machine().lower() in {"x86_64", "amd64"} else "arm64")
    )

    class Runner(ios.SubprocessCommandRunner):
        def run(self, command, **kwargs):
            # Save ownership BEFORE boot/install so an interrupted helper can
            # still be cleaned up by the guest supervisor.
            if list(command[:3]) == ["xcrun", "simctl", "boot"]:
                state = _read_state(run_dir)
                state["runtime"] = {"udid": command[3], "bundle_id": contract.bundle_identifier}
                _write_json(run_dir / "platform.json", state)
            if list(command[:3]) == ["xcrun", "simctl", "install"]:
                state = _read_state(run_dir)
                state["runtime"]["installed"] = True
                _write_json(run_dir / "platform.json", state)
            if list(command[:3]) == ["xcrun", "simctl", "shutdown"]:
                state = _read_state(run_dir)
                if state["runtime"].get("installed"):
                    result = super().run(["xcrun", "simctl", "uninstall", command[3], contract.bundle_identifier], **kwargs)
                    if result.returncode:
                        raise ios.IOSSimulatorAppContractError("Simulator app uninstall failed")
                    state["runtime"]["installed"] = False
                    _write_json(run_dir / "platform.json", state)
            return super().run(command, **kwargs)

    runner = Runner()
    budget = ios.RunBudget(max_seconds=timeout, cleanup_reserve_seconds=min(120, timeout / 4))
    if simulator_mode == "metal":
        ios.require_metal(runner, budget=budget)
    work = run_dir / "work" / "ios"
    app = ios.prepare_ios_simulator_candidate(
        candidate_root=run_dir / "source", work_dir=work, runner=runner,
        contract=contract, mode=simulator_mode, budget=budget,
    )
    evidence = ios.run_ios_simulator_app_contract(
        candidate_root=run_dir / "source", work_dir=work, runner=runner,
        existing_app=app if simulator_mode == "mini" else None,
        contract=contract, budget=budget,
        mode=simulator_mode,
        diagnostic_dir=logs / "ios",
    )
    _write_json(logs / "simulator.json", {
        "scope": f"ios-simulator-{simulator_mode}", "mode": simulator_mode, "passed": True,
        "udid": evidence.simulator.udid, "architecture": contract.architecture,
    })
    return _read_state(run_dir)["runtime"]


def cleanup(run_dir: Path, runtime: dict, logs: Path, timeout: float) -> None:
    from .local_vm import _run_logged, _read_state, _write_json, LocalVMError

    udid = runtime.get("udid")
    if not udid:
        return
    # The existing helper normally shuts down its Simulator itself. On
    # interruption, only the recorded device may need stopping.
    inventory = _run_logged(["xcrun", "simctl", "list", "devices", "-j"], cwd=run_dir,
                            logs=logs, label="ios-cleanup-inventory", timeout=timeout)
    devices = [device for values in json.loads(inventory.stdout)["devices"].values() for device in values]
    selected = next((device for device in devices if device["udid"].upper() == udid.upper()), None)
    if selected is None:
        raise LocalVMError("recorded Simulator is missing; inspect guest state")
    booted = selected["state"] != "Shutdown"
    if runtime.get("installed"):
        if not booted:
            _run_logged(["xcrun", "simctl", "boot", udid], cwd=run_dir,
                        logs=logs, label="ios-cleanup-boot", timeout=timeout)
            _run_logged(["xcrun", "simctl", "bootstatus", udid, "-b"], cwd=run_dir,
                        logs=logs, label="ios-cleanup-ready", timeout=timeout)
            booted = True
        _run_logged(["xcrun", "simctl", "uninstall", udid, runtime["bundle_id"]], cwd=run_dir,
                    logs=logs, label="ios-cleanup-uninstall", timeout=timeout)
        state = _read_state(run_dir)
        state["runtime"]["installed"] = False
        _write_json(run_dir / "platform.json", state)
    if booted:
        _run_logged(["xcrun", "simctl", "shutdown", udid], cwd=run_dir,
                    logs=logs, label="ios-cleanup-shutdown", timeout=timeout)
