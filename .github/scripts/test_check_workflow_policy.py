#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).with_name("check_workflow_policy.py")
SPEC = importlib.util.spec_from_file_location("check_workflow_policy", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load {SCRIPT}")
policy = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(policy)


PREPARE = "Prepare F-Droid-compatible tool paths"
SETUP = "Set up bootstrap Go"


class WorkflowStepOrderTests(unittest.TestCase):
    def test_accepts_actual_steps_in_required_order(self) -> None:
        source = f"""
      - name: {PREPARE}
        run: mkdir -p /tmp/example
      - name: {SETUP}
        uses: actions/setup-go@v6
"""
        self.assertIsNone(policy._step_order_violation(source, PREPARE, SETUP))

    def test_rejects_actual_steps_in_reverse_order(self) -> None:
        source = f"""
      - name: {SETUP}
        uses: actions/setup-go@v6
      - name: {PREPARE}
        run: mkdir -p /tmp/example
"""
        self.assertIn(
            "must run before", policy._step_order_violation(source, PREPARE, SETUP)
        )

    def test_comment_cannot_substitute_for_prepare_step(self) -> None:
        source = f"""
      # - name: {PREPARE}
      - name: {SETUP}
        uses: actions/setup-go@v6
"""
        self.assertIn(
            "exactly one actual workflow step",
            policy._step_order_violation(source, PREPARE, SETUP),
        )

    def test_comment_cannot_substitute_for_setup_step(self) -> None:
        source = f"""
      - name: {PREPARE}
        run: mkdir -p /tmp/example
      # - name: {SETUP}
"""
        self.assertIn(
            "exactly one actual workflow step",
            policy._step_order_violation(source, PREPARE, SETUP),
        )


class ImmutableActionReferenceTests(unittest.TestCase):
    def test_rejects_mutable_external_action_reference(self) -> None:
        match = policy.EXTERNAL_ACTION.search("      - uses: hydraulic-software/conveyor/actions/build@v16.0\n")
        self.assertIsNotNone(match)
        self.assertIsNone(policy.re.fullmatch(policy.FULL_SHA, match.group("ref")))

    def test_accepts_full_commit_action_reference(self) -> None:
        match = policy.EXTERNAL_ACTION.search(
            "      - uses: actions/checkout@" + "a" * 40 + " # v5\n"
        )
        self.assertIsNotNone(match)
        self.assertIsNotNone(policy.re.fullmatch(policy.FULL_SHA, match.group("ref")))


if __name__ == "__main__":
    unittest.main()
