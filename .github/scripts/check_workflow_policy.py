#!/usr/bin/env python3
"""Fail closed on workflow shapes that could expose release secrets to PR code.

This intentionally uses only the standard library so it can run in every
secretless verification job. It validates the repository's small workflow
policy rather than attempting to execute or fully interpret GitHub Actions.
"""
from __future__ import annotations

from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = ROOT / "workflows"
PR_TRIGGER = re.compile(r"^\s{2}pull_request\s*:", re.MULTILINE)
SECRET = re.compile(r"\$\{\{\s*secrets\.([A-Za-z0-9_]+)\s*\}\}")


def main() -> int:
    violations: list[str] = []
    for workflow in sorted(WORKFLOWS.glob("*.yml")):
        text = workflow.read_text(encoding="utf-8")
        if "secrets: inherit" in text:
            violations.append(f"{workflow.name}: secrets: inherit is forbidden; pass named secrets only")
        if PR_TRIGGER.search(text):
            exposed = sorted({name for name in SECRET.findall(text) if name != "GITHUB_TOKEN"})
            if exposed:
                violations.append(
                    f"{workflow.name}: pull_request workflow references non-GITHUB_TOKEN secrets: {', '.join(exposed)}"
                )
    release = (WORKFLOWS / "release.yml").read_text(encoding="utf-8")
    if PR_TRIGGER.search(release):
        violations.append("release.yml: protected release workflow must not run on pull_request")
    for name in ("android_build.yml", "desktop_build.yml", "ios_build.yml", "desktop_cli_tests.yml"):
        text = (WORKFLOWS / name).read_text(encoding="utf-8")
        if not re.search(r"^\s{2}workflow_call\s*:", text, re.MULTILINE):
            violations.append(f"{name}: protected workflow must expose only an explicit workflow_call interface")
        if PR_TRIGGER.search(text):
            violations.append(f"{name}: protected workflow must not be directly PR-triggered")
        if "environment: release" not in text:
            violations.append(f"{name}: secret-consuming job must require the protected release environment")

    # macOS desktop artifacts are native binaries. Keep the two official runner
    # architectures and every consumer explicitly paired so an ARM service can
    # never silently land in an Intel package (or vice versa).
    desktop_libs = (WORKFLOWS / "desktop_libs_generate.yml").read_text(encoding="utf-8")
    for runner, arch in (("macos-15", "arm64"), ("macos-15-intel", "amd64")):
        if not re.search(
            rf"runner:\s*{re.escape(runner)}\s+platform:\s*macos\s+arch:\s*{arch}",
            desktop_libs,
        ):
            violations.append(
                f"desktop_libs_generate.yml: macOS {arch} must use official {runner} runner"
            )
    if "name: macos_grpcvpnserver-${{ matrix.arch }}" not in desktop_libs:
        violations.append("desktop_libs_generate.yml: macOS service artifact name must include matrix.arch")

    desktop_cli = (WORKFLOWS / "desktop_cli_tests.yml").read_text(encoding="utf-8")
    if not re.search(
        r"os:\s*macos-15-intel\s+arch:\s*amd64\s+app_artifact:\s*dobbyVPN-macos-amd64\.zip\s+service_artifact:\s*macos_grpcvpnserver-amd64",
        desktop_cli,
    ):
        violations.append("desktop_cli_tests.yml: Intel macOS lane must use matching amd64 app and service artifacts")

    installers = (WORKFLOWS / "installers_build.yml").read_text(encoding="utf-8")
    for arch in ("arm64", "amd64"):
        expected = f"name: macos_grpcvpnserver-{arch}"
        destination = f"path: installer/macos/services/{arch}"
        if expected not in installers or destination not in installers:
            violations.append(
                f"installers_build.yml: macOS {arch} service must be downloaded into its architecture-specific path"
            )
    if violations:
        print("Workflow secret-isolation policy failed:", file=sys.stderr)
        print("\n".join(f"- {item}" for item in violations), file=sys.stderr)
        return 1
    print("Workflow secret-isolation policy passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
