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


class ActiveToolPinTests(unittest.TestCase):
    def test_current_product_tool_entry_points_are_exactly_pinned(self) -> None:
        self.assertEqual(policy._active_tool_pin_violations(), [])

    def test_policy_recognizes_a_mutable_tool_reference(self) -> None:
        self.assertIsNotNone(
            policy.MUTABLE_GO_TOOL_COMMAND.search(
                "go install example.invalid/tool@latest"
            )
        )


class HostedPlatformBoundTests(unittest.TestCase):
    def test_product_platform_workflows_have_the_hard_30_minute_ceiling(self) -> None:
        for name in (
            "android_build.yml",
            "desktop_build.yml",
            "desktop_libs_generate.yml",
            "installers_build.yml",
            "ios_build.yml",
        ):
            with self.subTest(workflow=name):
                source = (policy.WORKFLOWS / name).read_text(encoding="utf-8")
                self.assertEqual(
                    policy.re.findall(
                        r"^\s{4}timeout-minutes:\s*(\d+)\s*$", source, policy.re.MULTILINE
                    ),
                    ["30"],
                )

    def test_functional_test_matrix_has_the_hard_30_minute_ceiling(self) -> None:
        source = (policy.WORKFLOWS / "test.yml").read_text(encoding="utf-8")
        runners = policy.re.findall(r"^\s{4}runs-on:\s*.+$", source, policy.re.MULTILINE)
        timeouts = policy.re.findall(
            r"^\s{4}timeout-minutes:\s*(\d+)\s*$", source, policy.re.MULTILINE
        )
        self.assertEqual(timeouts, ["30"] * len(runners))

    def test_go_race_gate_targets_current_runtime_packages(self) -> None:
        source = (policy.WORKFLOWS / "test.yml").read_text(encoding="utf-8")
        self.assertIn(
            "go test -race ./routing/... ./sessionapi/... ./tunnel/...",
            source,
        )
        self.assertNotIn("go test -race ./core", source)


class DiagnosticOutputPolicyTests(unittest.TestCase):
    def test_derived_toolchain_checks_duplicate_complete_output_before_filtering(self) -> None:
        sources = {
            "android_build.yml": (policy.WORKFLOWS / "android_build.yml").read_text(encoding="utf-8"),
            "desktop_libs_generate.yml": (policy.WORKFLOWS / "desktop_libs_generate.yml").read_text(encoding="utf-8"),
            "ios_libs_generate.yml": (policy.WORKFLOWS / "ios_libs_generate.yml").read_text(encoding="utf-8"),
            "android_build_driver.sh": (SCRIPT.parent / "android_build_driver.sh").read_text(encoding="utf-8"),
        }
        for name, source in sources.items():
            with self.subTest(source=name):
                self.assertNotRegex(source, r"2>&1\s*\|\s*(?:grep|awk|sed)")
                self.assertNotRegex(source, r"version -m[^\n]*2>&1[^\n]*\|\s*grep")
        for name in sources:
            with self.subTest(source=name):
                self.assertIn("tee /dev/stderr", sources[name])

    def test_android_driver_uses_fresh_non_overwriting_evidence_files(self) -> None:
        source = (SCRIPT.parent / "android_build_driver.sh").read_text(encoding="utf-8")
        self.assertIn('stdout.original.XXXXXX.log', source)
        self.assertIn('stderr.original.XXXXXX.log', source)
        self.assertIn('chmod 600 "$stdout_original" "$stderr_original"', source)
        self.assertNotIn('tee -- "$evidence_dir/stdout.original.log"', source)
        self.assertNotIn('tee -- "$evidence_dir/stderr.original.log"', source)


class AndroidLegacyHelperContractTests(unittest.TestCase):
    def test_android_workflow_binds_trusted_helpers_to_workflow_sha(self) -> None:
        source = (policy.WORKFLOWS / "android_build.yml").read_text(encoding="utf-8")
        for expected in (
            "Check out trusted Android build helpers",
            "Verify trusted Android helper checkout before any trusted helper use",
            "ref: ${{ github.workflow_sha }}",
            "TRUSTED_HELPER_SHA: ${{ github.workflow_sha }}",
            "--trusted-helper-root",
            "--trusted-helper-sha",
            ".github/android/dependency-spec.json",
            ".github/scripts/android_build_driver.sh",
            ".github/scripts/android_dependency_provenance.py",
            ".github/scripts/verify_android_apk_source.py",
            ".github/scripts/verify_android_reproducibility.py",
            "Download and verify trusted Gradle distribution",
            "--verify-gradle-distribution",
            "--gradle-archive",
            "--gradle-root",
            "GRADLE_BIN",
            "status --porcelain=v2 --untracked-files=all --ignored",
            "find \"$TRUSTED_HELPER_ROOT\" -mindepth 1 -print0",
            "trusted helper checkout is not clean",
            "trusted helper checkout contains a symlink",
            "trusted helper file is missing or not a regular non-symlink file",
        ):
            with self.subTest(expected=expected):
                self.assertIn(expected, source)
        self.assertNotIn(
            '--dependency-spec "$FDROID_COMPAT_SOURCE_ROOT/.github/android/dependency-spec.json"',
            source,
        )

    def test_inline_helper_gate_precedes_every_trusted_android_use(self) -> None:
        source = (policy.WORKFLOWS / "android_build.yml").read_text(encoding="utf-8")
        gate = "Verify trusted Android helper checkout before any trusted helper use"
        for after in (
            "Build Go from source for Android",
            "Download and verify trusted Gradle distribution",
            "Install gomobile",
            "Verify reproducible Android toolchain",
        ):
            with self.subTest(after=after):
                self.assertIsNone(policy._step_order_violation(source, gate, after))

    def test_policy_order_helper_rejects_trusted_use_before_inline_gate(self) -> None:
        gate = "Verify trusted Android helper checkout before any trusted helper use"
        source = f"""
      - name: Build Go from source for Android
        run: python3 trusted-helper
      - name: {gate}
        run: validate
"""
        self.assertIn(
            "must run before",
            policy._step_order_violation(source, gate, "Build Go from source for Android"),
        )

    def test_driver_has_a_fail_closed_separate_helper_contract(self) -> None:
        source = (Path(__file__).with_name("android_build_driver.sh")).read_text(encoding="utf-8")
        for expected in (
            "--validate-helper-contract",
            "trusted helper checkout SHA mismatch",
            "trusted helper root must be a non-symlink directory",
            "explicit dependency helper is not the trusted helper checkout",
            "Android build modified tracked source files",
            "--verify-manifest",
            "--verify-gradle-distribution",
            "--gradle-archive",
            "--gradle-root",
        ):
            with self.subTest(expected=expected):
                self.assertIn(expected, source)
        self.assertGreaterEqual(source.count("verify_source_integrity_after_build"), 4)


if __name__ == "__main__":
    unittest.main()
