#!/usr/bin/env python3
"""Verify the JVM presentation tests reject mutations to production behavior."""

from __future__ import annotations

import hashlib
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path


ANDROID_ROOT = Path(__file__).resolve().parent
PRODUCT_ROOT = ANDROID_ROOT.parent.parent
GRADLE_COMMAND = ("./gradlew", ":presentation:test", "--rerun-tasks")


MUTATIONS = (
    (
        "typed debounce deadline",
        Path("presentation/src/main/kotlin/com/dobby/presentation/SubscriptionLoadCoordinator.kt"),
        "TYPED(400),",
        "TYPED(401),",
        "typedEditStartsAt400Milliseconds",
    ),
    (
        "log follow scroll decision",
        Path("presentation/src/main/kotlin/com/dobby/presentation/LogFollowState.kt"),
        "followsLatest = !canScrollDown",
        "followsLatest = canScrollDown",
        "scrollAwayPausesFollowingAndReturningToBottomResumesIt",
    ),
    (
        "permission source-dirty fence",
        Path("presentation/src/main/kotlin/com/dobby/presentation/PermissionTargetFence.kt"),
        "!snapshot.sourceDirty &&",
        "snapshot.sourceDirty &&",
        "capturedTargetSurvivesConsentOnlyWhileItsIdentityIsCurrent",
    ),
)


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run_tests(project: Path, label: str) -> tuple[int, bytes, bytes]:
    print(f"\n=== {label}: {' '.join(GRADLE_COMMAND)} ===", flush=True)
    completed = subprocess.run(
        GRADLE_COMMAND,
        cwd=project,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    # Replay complete child output before the temporary project is removed.
    sys.stdout.buffer.write(completed.stdout)
    sys.stdout.buffer.flush()
    sys.stderr.buffer.write(completed.stderr)
    sys.stderr.buffer.flush()
    return completed.returncode, completed.stdout, completed.stderr


def expected_assertion_failure(project: Path, test_name: str) -> bool:
    reports = project / "presentation" / "build" / "test-results" / "test"
    if not reports.is_dir():
        return False
    for report in reports.glob("TEST-*.xml"):
        try:
            root = ET.parse(report).getroot()
        except (ET.ParseError, OSError):
            continue
        for case in root.findall("testcase"):
            if case.get("name") != test_name:
                continue
            failure = case.find("failure")
            if failure is None:
                return False
            return failure.get("type", "").endswith("AssertionError") or failure.get(
                "type", ""
            ).endswith("ComparisonFailure")
    return False


def copy_project(destination: Path) -> None:
    destination.mkdir(parents=True)
    android_project = destination / "ui" / "android"
    shutil.copytree(
        ANDROID_ROOT,
        android_project,
        ignore=shutil.ignore_patterns(
            ".gradle",
            "build",
            "local.properties",
            ".idea",
            "*.apk",
            "*.aab",
        ),
    )
    shutil.copy2(PRODUCT_ROOT / ".go-version", destination / ".go-version")


def main() -> int:
    original_hashes = {
        relative: sha256(ANDROID_ROOT / relative) for _, relative, *_ in MUTATIONS
    }

    baseline_code, _, _ = run_tests(ANDROID_ROOT, "unmodified presentation tests")
    if baseline_code != 0:
        print("The unmodified presentation tests failed; mutation checks were not run.", file=sys.stderr)
        return baseline_code or 1

    with tempfile.TemporaryDirectory(prefix="dobbyvpn-presentation-mutations-") as temporary:
        temp_root = Path(temporary)
        for index, (label, relative, old, new, test_name) in enumerate(MUTATIONS):
            project_root = temp_root / f"case-{index}"
            copy_project(project_root)
            project = project_root / "ui" / "android"
            source = project / relative
            contents = source.read_text(encoding="utf-8")
            if contents.count(old) != 1:
                print(
                    f"Cannot apply {label} mutation: expected one production source match, "
                    f"found {contents.count(old)} in {relative}.",
                    file=sys.stderr,
                )
                return 1
            source.write_text(contents.replace(old, new, 1), encoding="utf-8")

            result, _, _ = run_tests(project, f"mutated {label}")
            if result == 0:
                print(
                    f"The presentation tests accepted the {label} production mutation.",
                    file=sys.stderr,
                )
                return 1
            if not expected_assertion_failure(project, test_name):
                print(
                    f"The {label} mutation did not reach its expected assertion failure "
                    f"({test_name}); inspect the complete Gradle output above.",
                    file=sys.stderr,
                )
                return 1
            print(f"Confirmed: {test_name} rejects the {label} mutation.", flush=True)

    changed_sources = [
        str(relative)
        for relative, expected in original_hashes.items()
        if sha256(ANDROID_ROOT / relative) != expected
    ]
    if changed_sources:
        print(
            "Production sources changed during isolated mutation checks: "
            + ", ".join(changed_sources),
            file=sys.stderr,
        )
        return 1
    print("All isolated production mutations were rejected; checkout sources are unchanged.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
