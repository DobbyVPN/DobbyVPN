#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).with_name("select_ios_simulator.py")
SPEC = importlib.util.spec_from_file_location("select_ios_simulator", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
selector = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(selector)


class SelectIosSimulatorTests(unittest.TestCase):
    def test_prefers_newest_runtime_then_preferred_model(self) -> None:
        devices = {
            "com.apple.CoreSimulator.SimRuntime.iOS-18-5": [
                {"name": "iPhone 15", "udid": "older", "isAvailable": True},
            ],
            "com.apple.CoreSimulator.SimRuntime.iOS-26-2": [
                {"name": "iPhone 17", "udid": "fallback", "isAvailable": True},
                {"name": "iPhone 16", "udid": "preferred", "isAvailable": True},
                {"name": "iPhone 16 Pro", "udid": "best", "isAvailable": True},
            ],
        }
        self.assertEqual(selector.select_simulator(devices), "best")

    def test_ignores_unavailable_and_non_iphone_devices(self) -> None:
        devices = {
            "com.apple.CoreSimulator.SimRuntime.iOS-26-2": [
                {"name": "iPhone 16 Pro", "udid": "unavailable", "isAvailable": False},
                {"name": "iPad Pro", "udid": "tablet", "isAvailable": True},
                {"name": "iPhone 17", "udid": "phone", "isAvailable": True},
            ],
        }
        self.assertEqual(selector.select_simulator(devices), "phone")

    def test_rejects_missing_available_iphone(self) -> None:
        with self.assertRaises(ValueError):
            selector.select_simulator({"com.apple.CoreSimulator.SimRuntime.iOS-26-2": []})


if __name__ == "__main__":
    unittest.main()
