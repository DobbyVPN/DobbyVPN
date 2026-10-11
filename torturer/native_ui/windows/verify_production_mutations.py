#!/usr/bin/env python3
"""Verify native Windows model tests reject isolated production-code mutations."""
from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


REPOSITORY = Path(__file__).resolve().parents[3]
NATIVE_SOURCE = REPOSITORY / "torturer" / "native_ui" / "windows"
PRODUCT_SOURCE = REPOSITORY / "ui" / "windows" / "DobbyVPN.Windows"


class MutationCheckError(RuntimeError):
    pass


MUTATIONS = (
    (
        "frozen-log-snapshot",
        "LogPresentationState.cs",
        "        _latestEntries = entries.ToList();\n        if (FollowsTail) ShowLatestEntries();",
        "        _latestEntries = entries.ToList();\n        if (true) ShowLatestEntries();",
        "a controlled append changed the frozen visible snapshot or discarded its expanded row",
    ),
    (
        "clear-boundary",
        "LogPresentationState.cs",
        "        _latestEntries = [];\n        _visibleEntries = [];\n        _expandedEntries.Clear();\n        FollowTail();",
        "        _latestEntries = _latestEntries;\n        _visibleEntries = [];\n        _expandedEntries.Clear();\n        FollowTail();",
        "Clear did not reset the production presentation state",
    ),
    (
        "typed-debounce",
        "SubscriptionLoadState.cs",
        "internal const int DebounceMilliseconds = 400;",
        "internal const int DebounceMilliseconds = 401;",
        "typed URL did not dispatch when its 400 ms debounce elapsed",
    ),
    (
        "pending-configure-dispatch",
        "SubscriptionLoadState.cs",
        "        if (_pending is not { } next) return null;\n        _pending = null;\n        _inFlight = next;\n        return next;\n    }\n\n    internal bool CanAct(",
        "        if (_pending is not { } next) return null;\n        if (next.Revision > request.Revision) { _pending = null; return null; }\n        _pending = null;\n        _inFlight = next;\n        return next;\n    }\n\n    internal bool CanAct(",
        "finishing a held load did not dispatch the latest immediate request",
    ),
    (
        "profile-semantic-id",
        "ProfileAutomationIds.cs",
        "index.ToString(CultureInfo.InvariantCulture)",
        "(index + 1).ToString(CultureInfo.InvariantCulture)",
        "profile role identifiers did not use the accepted digest and zero-based inventory index",
    ),
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dotnet", default=shutil.which("dotnet") or "dotnet")
    return parser.parse_args()


def copy_isolated_project(destination: Path) -> Path:
    root = destination / "worktree"
    native = root / "torturer" / "native_ui" / "windows"
    product = root / "ui" / "windows" / "DobbyVPN.Windows"
    native.mkdir(parents=True)
    product.mkdir(parents=True)

    for source in NATIVE_SOURCE.iterdir():
        if source.is_file() and source.suffix in {".cs", ".csproj"}:
            shutil.copy2(source, native / source.name)
    for name in (
        "NativeDiagnostics.cs",
        "LogPresentationState.cs",
        "SubscriptionLoadState.cs",
        "ProfileAutomationIds.cs",
    ):
        shutil.copy2(PRODUCT_SOURCE / name, product / name)
    return native


def run_forwarded(command: list[str], cwd: Path, label: str) -> subprocess.CompletedProcess[bytes]:
    print(f"[windows-mutation-check] {label}: {' '.join(command)}", flush=True)
    result = subprocess.run(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    sys.stdout.buffer.write(result.stdout)
    sys.stdout.buffer.flush()
    sys.stderr.buffer.write(result.stderr)
    sys.stderr.buffer.flush()
    return result


def build_and_run(dotnet: str, project_directory: Path, mutation: str | None, expected_failure: str | None) -> None:
    built = run_forwarded(
        [dotnet, "build", "NativeUI.csproj", "--configuration", "Release", "--nologo", "--verbosity", "minimal"],
        project_directory,
        f"compile {mutation or 'baseline'}",
    )
    if built.returncode != 0:
        raise MutationCheckError(
            f"{mutation or 'baseline'} compile failed with exit code {built.returncode}; mutation result is inconclusive"
        )

    executable = project_directory / "bin" / "Release" / "net10.0-windows" / "NativeUI.exe"
    if not executable.is_file():
        raise MutationCheckError(f"successful {mutation or 'baseline'} build did not produce {executable}")
    tested = run_forwarded([str(executable), "--diagnostics-test"], project_directory,
                           f"run existing diagnostics-test for {mutation or 'baseline'}")
    if mutation is None:
        if tested.returncode != 0:
            raise MutationCheckError(f"unmutated diagnostics-test failed with exit code {tested.returncode}")
        return

    output = (tested.stdout + tested.stderr).decode("utf-8", errors="replace")
    if tested.returncode == 0:
        raise MutationCheckError(f"production mutation {mutation} survived diagnostics-test")
    if expected_failure not in output:
        raise MutationCheckError(
            f"production mutation {mutation} exited {tested.returncode} without its expected assertion failure: "
            f"{expected_failure}"
        )
    print(f"[windows-mutation-check] rejected {mutation} with the expected assertion failure", flush=True)


def main() -> int:
    args = parse_args()
    if sys.platform != "win32":
        print("[windows-mutation-check] ERROR: run this optional check on Windows", file=sys.stderr)
        return 2
    if not NATIVE_SOURCE.is_dir() or not PRODUCT_SOURCE.is_dir():
        print("[windows-mutation-check] ERROR: expected product source tree was not found", file=sys.stderr)
        return 2

    try:
        with tempfile.TemporaryDirectory(prefix="dobbyvpn-windows-mutations-") as temporary:
            temporary_root = Path(temporary)
            baseline = copy_isolated_project(temporary_root / "baseline")
            build_and_run(args.dotnet, baseline, None, None)

            for name, file_name, original, replacement, expected_failure in MUTATIONS:
                case_root = temporary_root / name
                project = copy_isolated_project(case_root)
                source_path = project.parents[2] / "ui" / "windows" / "DobbyVPN.Windows" / file_name
                source = source_path.read_text(encoding="utf-8")
                replacements = source.count(original)
                if replacements != 1:
                    raise MutationCheckError(
                        f"{name} expected one production source replacement, found {replacements}; "
                        "no mutation was built"
                    )
                source_path.write_text(source.replace(original, replacement, 1), encoding="utf-8", newline="")
                build_and_run(args.dotnet, project, name, expected_failure)

        print(f"[windows-mutation-check] rejected all {len(MUTATIONS)} production mutations", flush=True)
        return 0
    except (OSError, MutationCheckError) as error:
        print(f"[windows-mutation-check] ERROR: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
