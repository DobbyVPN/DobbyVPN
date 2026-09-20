from __future__ import annotations

from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from unittest import mock

from torturer_checks.hosted import native_ui
from torturer_contract.functional.results import ConnectionIdentity


class _FakeBase:
    def __init__(self, *, process_loss_verified: bool = True) -> None:
        self.connected = False
        self.process_loss_verified = process_loss_verified
        self.executed: list[str] = []
        self.native_preparations = 0
        self.reset_calls = 0
        self.finalize_calls = 0

    def discover_connections(self, timeout_seconds: float):
        return (ConnectionIdentity(index=0, protocol="OUTLINE"),)

    def select_connection(self, _connection) -> None:
        pass

    def _capture_baseline(self, _timeout: float) -> None:
        pass

    def prepare_native_connect(self, _timeout: float) -> None:
        self.native_preparations += 1

    def _connected(self, _timeout: float) -> bool:
        return self.connected

    def _cleanup_verified(self, _timeout: float) -> bool:
        return not self.connected

    def execute(self, step):
        self.executed.append(step.operation)
        if step.operation == "process_loss":
            self.connected = True
            return {"process_loss_verified": self.process_loss_verified}
        if step.operation == "observe_tunnel":
            return {"tunnel_interface": True}
        if step.operation == "observe_routing_identity":
            return {"routing_verified": True}
        if step.operation == "measure_stability":
            return {"stability_verified": True}
        if step.operation == "measure_throughput":
            return {"throughput_positive": True}
        return {}

    def reset(self, timeout_seconds: float) -> None:
        self.reset_calls += 1
        self.connected = False

    def finalize(self, timeout_seconds: float) -> None:
        self.finalize_calls += 1


class _FakeUI:
    def __init__(self, *, settings_verified: bool = True, recovery_verified: bool = True, **_kwargs) -> None:
        self.base: _FakeBase | None = None
        self.operations: list[str] = []
        self.settings_verified = settings_verified
        self.recovery_verified = recovery_verified

    def start(self) -> None:
        pass

    def request(self, operation: str, **_kwargs):
        self.operations.append(operation)
        if operation in {"connect", "reconnect", "reopen"} and self.base is not None:
            self.base.connected = True
        if operation == "disconnect" and self.base is not None:
            self.base.connected = False
        if operation == "settings":
            return {
                "settings_version": self.settings_verified,
                "settings_source_commit": self.settings_verified,
            }
        if operation == "process_loss_recovery":
            return {
                "status": "Connected" if self.recovery_verified else "Disconnected",
                "reconnecting_seen": self.recovery_verified,
            }
        return {"status": "Connected"}

    def close(self) -> None:
        pass


class NativeUIJourneyTests(unittest.TestCase):
    def test_native_actions_wrap_base_observations_and_fault_injection(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile"
            profile.write_text("synthetic", encoding="utf-8")
            cli = root / "cli"
            ui = root / "ui"
            smoke = root / "smoke.py"
            for path in (cli, ui, smoke):
                path.write_text("candidate", encoding="utf-8")
            base = _FakeBase()
            fake_ui = _FakeUI()
            fake_ui.base = base
            args = SimpleNamespace(
                platform="windows",
                cli=cli,
                ui=ui,
                profile=profile,
                smoke_script=smoke,
                raw_log_dir=root / "logs",
                timeout=30.0,
                service_pid=42,
                service_binary=ui,
                service_socket="127.0.0.1:50051",
                service_library_path=None,
                service_pid_file=None,
                service_identity_file=None,
                network_interface="7",
                routing_firewall_helper=None,
                network_transition_helper=None,
            )
            with (
                mock.patch.object(native_ui, "adapter_for_platform", return_value=base),
                mock.patch.object(native_ui, "_NativeUIProcess", return_value=fake_ui),
            ):
                result = native_ui.run_journey(args)
            self.assertTrue(result["complete"])
            self.assertIn("measure_throughput", base.executed)
            self.assertIn("process_loss", base.executed)
            self.assertNotIn("connect", base.executed)
            self.assertNotIn("disconnect", base.executed)
            self.assertEqual(base.native_preparations, 2)
            self.assertIn("connect", fake_ui.operations)
            self.assertIn("disconnect", fake_ui.operations)
            self.assertIn("reconnect", fake_ui.operations)
            self.assertIn("process_loss_recovery", fake_ui.operations)
            self.assertEqual(base.reset_calls, 1)
            self.assertEqual(base.finalize_calls, 1)

    def _run_with_fakes(self, base: _FakeBase, fake_ui: _FakeUI) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = root / "profile"
            profile.write_text("synthetic", encoding="utf-8")
            cli = root / "cli"
            ui = root / "ui"
            smoke = root / "smoke.py"
            for path in (cli, ui, smoke):
                path.write_text("candidate", encoding="utf-8")
            args = SimpleNamespace(
                platform="windows",
                cli=cli,
                ui=ui,
                profile=profile,
                smoke_script=smoke,
                raw_log_dir=root / "logs",
                timeout=30.0,
                service_pid=42,
                service_binary=ui,
                service_socket="127.0.0.1:50051",
                service_library_path=None,
                service_pid_file=None,
                service_identity_file=None,
                network_interface="7",
                routing_firewall_helper=None,
                network_transition_helper=None,
            )
            fake_ui.base = base
            with (
                mock.patch.object(native_ui, "adapter_for_platform", return_value=base),
                mock.patch.object(native_ui, "_NativeUIProcess", return_value=fake_ui),
            ):
                native_ui.run_journey(args)

    def test_required_settings_metadata_is_enforced(self) -> None:
        with self.assertRaisesRegex(native_ui.NativeUIJourneyError, "settings_source_commit"):
            self._run_with_fakes(_FakeBase(), _FakeUI(settings_verified=False))

    def test_required_process_loss_evidence_is_enforced(self) -> None:
        with self.assertRaisesRegex(native_ui.NativeUIJourneyError, "process_loss_verified"):
            self._run_with_fakes(_FakeBase(process_loss_verified=False), _FakeUI())


if __name__ == "__main__":
    unittest.main()
