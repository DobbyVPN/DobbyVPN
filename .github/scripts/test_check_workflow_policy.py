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


class AndroidBuildSurfaceTests(unittest.TestCase):
    def test_android_workflow_uses_the_public_driver_and_closure(self) -> None:
        source = (policy.WORKFLOWS / "android_build.yml").read_text(encoding="utf-8")
        self.assertIn("Build and verify isolated unsigned APKs through the public driver", source)
        self.assertIn("android_build_driver.sh", source)
        self.assertIn("--dependency-closure", source)
        self.assertIn(".github/android/dependency-closure.json", source)

    def test_android_driver_retains_fresh_non_overwriting_originals(self) -> None:
        source = (SCRIPT.parent / "android_build_driver.sh").read_text(encoding="utf-8")
        self.assertIn('stdout.original.XXXXXX.log', source)
        self.assertIn('stderr.original.XXXXXX.log', source)
        self.assertIn('chmod 600 "$stdout_original" "$stderr_original"', source)
        self.assertNotIn('tee -- "$evidence_dir/stdout.original.log"', source)
        self.assertNotIn('tee -- "$evidence_dir/stderr.original.log"', source)


if __name__ == "__main__":
    unittest.main()
