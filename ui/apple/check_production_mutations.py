#!/usr/bin/env python3
"""Verify deterministic Swift session-model tests reject production mutations."""

from __future__ import annotations

import hashlib
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


APPLE_ROOT = Path(__file__).resolve().parent
MODEL = Path("shared/ui/DobbySessionViewModel.swift")

MUTATIONS = (
    (
        "typed-debounce-boundary",
        "schedule(afterNanoseconds: 400_000_000, action: enqueueLoad)",
        "schedule(afterNanoseconds: 350_000_000, action: enqueueLoad)",
        "testTypedEditsWaitForDebounceAndOnlyConfigureLatestURL",
        "Only the latest typed URL should Configure at the debounce boundary",
    ),
    (
        "immediate-paste-bypass",
        "if immediate {\n            enqueueLoad()\n        } else {",
        "if immediate {\n            debounce = debounceScheduler.schedule(afterNanoseconds: 400_000_000, action: enqueueLoad)\n        } else {",
        "testPasteIsImmediateFencesOldResultAndLeavesStopAndLogsResponsive",
        "Paste must start the next serialized Configure without a typing debounce",
    ),
    (
        "stale-configure-result-fence",
        "if revision == self.loadRevision {",
        "if revision <= self.loadRevision {",
        "testPasteIsImmediateFencesOldResultAndLeavesStopAndLogsResponsive",
        "The old Configure result must not restore its superseded URL",
    ),
)


class MutationCheckError(RuntimeError):
    pass


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def copy_package(destination: Path) -> None:
    shutil.copytree(
        APPLE_ROOT,
        destination,
        ignore=shutil.ignore_patterns(".build", ".swiftpm", "DerivedData", "*.xcresult", ".DS_Store"),
    )


def run_test(swift: str, package: Path, method: str, label: str) -> subprocess.CompletedProcess[bytes]:
    command = [swift, "test", "--package-path", str(package), "--filter", method]
    print(f"\n[swift-mutation-check] {label}: {' '.join(command)}", flush=True)
    result = subprocess.run(command, cwd=package, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    # Keep full compiler and XCTest output in the normal source-check stream.
    sys.stdout.buffer.write(result.stdout)
    sys.stdout.buffer.flush()
    sys.stderr.buffer.write(result.stderr)
    sys.stderr.buffer.flush()
    return result


def require_test_outcome(result: subprocess.CompletedProcess[bytes], method: str, outcome: str) -> bytes:
    output = result.stdout + result.stderr
    case_prefix = f"Test Case '-[DobbyNativeUITests.SessionViewModelTests {method}]' "
    case_results = re.findall(re.escape(case_prefix.encode()) + rb"(passed|failed|skipped) \(", output)
    expected_failures = 0 if outcome == "passed" else None
    summaries = [int(value) for value in re.findall(
        rb"Executed 1 test, with (\d+) failures? \(", output
    )]
    compile_succeeded = b"Build complete!" in output
    failures_match = bool(summaries) and (
        all(value == expected_failures for value in summaries)
        if expected_failures is not None else all(value > 0 for value in summaries)
    )
    if (not compile_succeeded or case_results != [outcome.encode()] or not failures_match):
        raise MutationCheckError(
            f"selected XCTest {method} did not compile and execute with one {outcome} result; "
            "a compiler/build failure or unmatched test selection is inconclusive"
        )
    return output


def require_named_assertion(output: bytes, name: str, assertion: str) -> None:
    failure_lines = [line for line in output.decode("utf-8", errors="replace").splitlines()
                     if "XCTAssert" in line and assertion in line]
    if not failure_lines:
        raise MutationCheckError(
            f"{name} did not reach its named XCTest assertion {assertion!r}; "
            "the mutation result is inconclusive"
        )


def check_outcome_parser() -> None:
    """Keep the XCTest transcript parser fail-closed without requiring a Mac run."""
    method = "testPasteIsImmediateFencesOldResultAndLeavesStopAndLogsResponsive"
    prefix = f"Test Case '-[DobbyNativeUITests.SessionViewModelTests {method}]' "
    passed = subprocess.CompletedProcess(
        ["swift", "test"], 0,
        ("Build complete!\n" + prefix + "started.\n" + prefix + "passed (0.01 seconds).\n"
         "Executed 1 test, with 0 failures (0 unexpected)\n" * 2).encode(), b"",
    )
    require_test_outcome(passed, method, "passed")
    failed_seven = subprocess.CompletedProcess(
        ["swift", "test"], 1,
        ("Build complete!\n" + prefix + "started.\n"
         "XCTAssertTrue failed - Paste must start the next serialized Configure without a typing debounce\n"
         "XCTAssertTrue failed - Paste must start the next serialized Configure without a typing debounce\n"
         + prefix + "failed (9.09 seconds).\n"
         "Executed 1 test, with 7 failures (0 unexpected)\n" * 2).encode(), b"",
    )
    repeated_assertions = require_test_outcome(failed_seven, method, "failed")
    require_named_assertion(repeated_assertions, "immediate-paste-bypass", "Paste must start the next serialized Configure without a typing debounce")
    negatives = (
        subprocess.CompletedProcess(["swift", "test"], 1, b"error: unable to compile\nBuild failed\n", b""),
        subprocess.CompletedProcess(["swift", "test"], 0,
                                    b"Build complete!\nExecuted 0 tests, with 0 failures\n", b""),
        subprocess.CompletedProcess(
            ["swift", "test"], 1,
            ("Build complete!\n" + prefix + "started.\n" + prefix + "passed (0.01 seconds).\n"
             "Executed 1 test, with 0 failures (0 unexpected)\n").encode(), b"",
        ),
    )
    for result in negatives:
        try:
            require_test_outcome(result, method, "failed")
        except MutationCheckError:
            continue
        raise MutationCheckError("XCTest transcript parser accepted a build, selection, or surviving-mutant failure")
    try:
        require_named_assertion(repeated_assertions, "immediate-paste-bypass", "no matching assertion")
    except MutationCheckError:
        pass
    else:
        raise MutationCheckError("mutation assertion matcher accepted an unrelated XCTest failure")


def main() -> int:
    try:
        check_outcome_parser()
    except MutationCheckError as error:
        print(f"[swift-mutation-check] ERROR: {error}", file=sys.stderr)
        return 1
    if sys.platform != "darwin":
        print("[swift-mutation-check] ERROR: run this optional check on macOS", file=sys.stderr)
        return 2
    swift = shutil.which("swift")
    if swift is None:
        print("[swift-mutation-check] ERROR: Swift is required", file=sys.stderr)
        return 2
    source = APPLE_ROOT / MODEL
    if not source.is_file():
        print(f"[swift-mutation-check] ERROR: production source is missing: {source}", file=sys.stderr)
        return 2

    original_hash = sha256(source)
    try:
        with tempfile.TemporaryDirectory(prefix="dobbyvpn-swift-mutations-") as temporary:
            root = Path(temporary)
            baseline = root / "baseline"
            copy_package(baseline)
            for name, _, _, method, _ in MUTATIONS:
                result = run_test(swift, baseline, method, f"baseline {name}")
                if result.returncode != 0:
                    raise MutationCheckError(
                        f"unmodified production model failed {method} with exit code {result.returncode}"
                    )
                require_test_outcome(result, method, "passed")

            for index, (name, old, new, method, assertion) in enumerate(MUTATIONS):
                package = root / f"mutated-{index}"
                copy_package(package)
                model = package / MODEL
                contents = model.read_text(encoding="utf-8")
                matches = contents.count(old)
                if matches != 1:
                    raise MutationCheckError(
                        f"{name} expected one production source replacement, found {matches}"
                    )
                model.write_text(contents.replace(old, new, 1), encoding="utf-8")
                result = run_test(swift, package, method, f"mutated {name}")
                if result.returncode == 0:
                    raise MutationCheckError(f"production mutation {name} survived {method}")
                output = require_test_outcome(result, method, "failed")
                require_named_assertion(output, name, assertion)
                print(f"[swift-mutation-check] confirmed {method} rejects {name}", flush=True)

        if sha256(source) != original_hash:
            raise MutationCheckError("the source checkout changed during isolated mutation checks")
        print(f"[swift-mutation-check] rejected all {len(MUTATIONS)} production mutations", flush=True)
        return 0
    except (OSError, MutationCheckError) as error:
        print(f"[swift-mutation-check] ERROR: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
