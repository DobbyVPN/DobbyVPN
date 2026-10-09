"""Secretless Simulator lane using the existing app lifecycle checks."""
from __future__ import annotations

import json
from pathlib import Path
import platform
import sys

from . import ios_simulator_app as ios
from .native_cases import IOS_RENDERER_SEVERITY_CASE, IOS_SUBSCRIPTION_FIXTURE_CASE


def prepare(
    run_dir: Path,
    logs: Path,
    timeout: float,
    architecture: str | None,
    source_sha: str | None = None,
) -> dict:
    contract = ios.public_ios_simulator_app_contract(
        architecture or ("amd64" if platform.machine().lower() in {"x86_64", "amd64"} else "arm64")
    )
    runner = ios.SubprocessCommandRunner()
    budget = ios.RunBudget(max_seconds=timeout, cleanup_reserve_seconds=min(120, timeout / 4))
    work = run_dir / "work" / "ios"
    try:
        ios.prepare_ios_simulator_candidate(
            candidate_root=run_dir / "source", work_dir=work, runner=runner,
            contract=contract, budget=budget, source_sha=source_sha,
        )
    except BaseException as error:
        try:
            ios.retain_ios_failure_diagnostic(work, error)
        except BaseException as report_error:
            error.add_note(
                f"iOS Simulator build failure report collection failed: {report_error}"
            )
        try:
            ios.retain_ios_diagnostics(work, logs / "ios-simulator")
        except BaseException as collection_error:
            error.add_note(
                f"iOS Simulator build diagnostic collection failed: {collection_error}"
            )
        raise
    return {
        "mode": "ios-simulator",
        "app": str(contract.app_path(work)),
        "architecture": contract.architecture,
        "source_sha": source_sha,
    }


def run(
    run_dir: Path,
    candidate: dict,
    logs: Path,
    timeout: float,
    native_cases: list[str] | None = None,
) -> dict:
    from .local_vm import _read_state, _write_json

    if candidate.get("mode") != "ios-simulator" or not isinstance(candidate.get("app"), str):
        raise ios.IOSSimulatorAppContractError("prepared iOS Simulator candidate is missing")
    screenshot_python = candidate.get("screenshot_python")
    screenshot_python_dir = run_dir / "screenshot-python"
    if (
        not isinstance(screenshot_python, str)
        or screenshot_python_dir.is_symlink()
        or not screenshot_python_dir.is_dir()
        or Path(screenshot_python).resolve() != screenshot_python_dir.resolve()
    ):
        raise ios.IOSSimulatorAppContractError("prepared iOS screenshot decoder is missing")
    package_path = str(screenshot_python_dir)
    if package_path not in sys.path:
        sys.path.insert(0, package_path)

    contract = ios.public_ios_simulator_app_contract(str(candidate.get("architecture", "")))
    work = run_dir / "work" / "ios"
    app_path = contract.app_path(work)
    if Path(candidate["app"]).resolve() != app_path.resolve() or not app_path.is_dir():
        raise ios.IOSSimulatorAppContractError("prepared iOS Simulator app descriptor is invalid")

    class Runner(ios.SubprocessCommandRunner):
        def run(self, command, **kwargs):
            arguments = list(command)
            # Save ownership BEFORE boot/install so an interrupted helper can
            # still be cleaned up by the guest supervisor.
            if arguments[:3] == ["xcrun", "simctl", "boot"]:
                state = _read_state(run_dir)
                runtime = state.get("runtime") or {}
                runtime.update({"udid": command[3], "bundle_id": contract.bundle_identifier})
                state["runtime"] = runtime
                _write_json(run_dir / "platform.json", state)
            if arguments[:3] == ["xcrun", "simctl", "install"]:
                state = _read_state(run_dir)
                state["runtime"]["installed"] = True
                _write_json(run_dir / "platform.json", state)
            if arguments[:3] == ["xcrun", "simctl", "shutdown"]:
                state = _read_state(run_dir)
                if state["runtime"].get("installed"):
                    result = super().run(["xcrun", "simctl", "uninstall", command[3], contract.bundle_identifier], **kwargs)
                    if result.returncode:
                        raise ios.IOSSimulatorAppContractError("Simulator app uninstall failed")
                    state["runtime"]["installed"] = False
                    _write_json(run_dir / "platform.json", state)
            result = super().run(command, **kwargs)
            if result.returncode == 0 and arguments[:3] == ["xcrun", "simctl", "create"]:
                state = _read_state(run_dir)
                state["runtime"] = {
                    "udid": result.stdout.strip(),
                    "bundle_id": contract.bundle_identifier,
                    "temporary": True,
                    "created": True,
                    "installed": False,
                }
                _write_json(run_dir / "platform.json", state)
            elif result.returncode == 0 and arguments[:3] == ["xcrun", "simctl", "delete"]:
                state = _read_state(run_dir)
                runtime = state.get("runtime") or {}
                runtime["created"] = False
                runtime["deleted"] = True
                runtime["installed"] = False
                state["runtime"] = runtime
                _write_json(run_dir / "platform.json", state)
            return result

    runner = Runner()
    budget = ios.RunBudget(max_seconds=timeout, cleanup_reserve_seconds=min(120, timeout / 4))
    try:
        evidence = ios.run_ios_simulator_app_contract(
            candidate_root=run_dir / "source", work_dir=work, runner=runner,
            contract=contract, budget=budget,
            native_cases=native_cases,
            source_sha=(
                candidate.get("source_sha")
                if isinstance(candidate.get("source_sha"), str)
                else None
            ),
        )
    except BaseException as error:
        # The contract retains artifacts before uninstall even when XCTest's
        # assertion is the primary failure. Copy them into the guest manifest
        # tree before the supervisor removes the disposable work directory;
        # retain the original exception before copying, and keep either
        # collection failure secondary to it.
        try:
            ios.retain_ios_failure_diagnostic(work, error)
        except BaseException as report_error:
            error.add_note(
                f"iOS Simulator failure report collection failed: {report_error}"
            )
        try:
            ios.retain_ios_diagnostics(work, logs / "ios-simulator")
        except BaseException as collection_error:
            error.add_note(
                f"iOS Simulator local diagnostic collection failed: {collection_error}"
            )
        raise
    ios.retain_ios_diagnostics(work, logs / "ios-simulator")
    selected_cases = native_cases or [
        "NativeUIInteractionTests",
        IOS_RENDERER_SEVERITY_CASE,
        IOS_SUBSCRIPTION_FIXTURE_CASE,
    ]
    _write_json(logs / "simulator.json", {
        "scope": "ios-simulator-mini", "suite": "mini", "passed": True,
        "udid": evidence.simulator.udid, "architecture": contract.architecture,
        "coverage": {
            "platform": "ios-simulator",
            "suite": "mini",
            "kind": "native-cases" if native_cases is not None else "suite",
            "native_case_selection": "explicit" if native_cases is not None else "suite-default",
            "native_cases": selected_cases,
            "xctest_filters": list(
                getattr(evidence, "selected_tests", ios.ui_test_selection(native_cases))
            ),
        },
    })
    return _read_state(run_dir)["runtime"]


def check_production(run_dir: Path, logs: Path, timeout: float, source_sha: str) -> None:
    from .local_vm import _run_logged

    _run_logged(
        [
            sys.executable,
            str(run_dir / "source" / ".github" / "scripts" / "ios" / "ios_production_check.py"),
            "--source-sha", source_sha,
            "--output-dir", str(run_dir / "work" / "ios-production"),
        ],
        cwd=run_dir / "source", logs=logs,
        label="ios-production-analysis-and-archive", timeout=timeout,
    )


def cleanup(run_dir: Path, runtime: dict, logs: Path, timeout: float) -> None:
    from .local_vm import _run_logged, _read_state, _write_json, LocalVMError

    udid = runtime.get("udid")
    if not udid:
        return
    if runtime.get("temporary"):
        from torturer_runner.subscription_fixture import SubscriptionFixture

        fixture_directory = run_dir / "work" / "ios" / "native-subscription-fixture"
        cleanup_errors: list[BaseException] = []

        if not runtime.get("deleted"):
            try:
                inventory = _run_logged(
                    ["xcrun", "simctl", "list", "devices", "-j"],
                    cwd=run_dir,
                    logs=logs,
                    label="ios-cleanup-inventory",
                    timeout=timeout,
                )
                devices = [
                    device
                    for values in json.loads(inventory.stdout)["devices"].values()
                    for device in values
                ]
                selected = next(
                    (device for device in devices if device["udid"].upper() == udid.upper()),
                    None,
                )
                if selected is not None:
                    if selected["state"] != "Shutdown":
                        try:
                            # Reset the disposable device trust before shutdown;
                            # deleting the device below is the final removal.
                            SubscriptionFixture.cleanup_interrupted(fixture_directory)
                        except BaseException as error:
                            cleanup_errors.append(error)
                        _run_logged(
                            ["xcrun", "simctl", "shutdown", udid],
                            cwd=run_dir,
                            logs=logs,
                            label="ios-cleanup-shutdown-temporary",
                            timeout=timeout,
                        )
                    _run_logged(
                        ["xcrun", "simctl", "delete", udid],
                        cwd=run_dir,
                        logs=logs,
                        label="ios-cleanup-delete-temporary",
                        timeout=timeout,
                    )
                state = _read_state(run_dir)
                state["runtime"]["created"] = False
                state["runtime"]["deleted"] = True
                state["runtime"]["installed"] = False
                _write_json(run_dir / "platform.json", state)
            except BaseException as error:
                cleanup_errors.append(error)

        try:
            # If deletion succeeded, the trust store is gone and the marker can
            # be removed without touching any reusable Simulator.
            SubscriptionFixture.cleanup_interrupted(fixture_directory)
        except BaseException as error:
            cleanup_errors.append(error)
        if cleanup_errors:
            raise ExceptionGroup("Disposable iOS Simulator cleanup failed", cleanup_errors)
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
