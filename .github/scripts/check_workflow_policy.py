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
FULL_SHA = r"[0-9a-f]{40}"


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

    promotion = (WORKFLOWS / "promote_release.yml").read_text(encoding="utf-8")
    if PR_TRIGGER.search(promotion):
        violations.append("promote_release.yml: public promotion must not run on pull_request")
    if SECRET.search(promotion) or "environment:" in promotion:
        violations.append(
            "promote_release.yml: GitHub/F-Droid promotion must not consume release secrets"
        )
    for expected in (
        "workflow_dispatch:",
        "actions: read",
        "contents: write",
        'test "$(jq -r .conclusion <<<"$run_json")" = "success"',
        'test "$(jq -r .headSha <<<"$run_json")" = "$RELEASE_SOURCE_SHA"',
        'test "$source_version" = "$RELEASE_VERSION"',
        "run-id: ${{ inputs.run_id }}",
        '--target "$RELEASE_SOURCE_SHA"',
    ):
        if expected not in promotion:
            violations.append(
                f"promote_release.yml: missing fail-closed promotion control: {expected}"
            )

    app_store = (WORKFLOWS / "submit_app_store.yml").read_text(encoding="utf-8")
    if PR_TRIGGER.search(app_store):
        violations.append(
            "submit_app_store.yml: production submission must not run on pull_request"
        )
    for expected in (
        "workflow_dispatch:",
        "actions: read",
        "contents: read",
        'test "$GITHUB_REF" = "refs/heads/main"',
        'test "$GITHUB_SHA" = "$current_main"',
        'test "$(jq -r .conclusion <<<"$run_json")" = "success"',
        'test "$(jq -r .headSha <<<"$run_json")" = "$RELEASE_SOURCE_SHA"',
        'test "$(jq -r .number <<<"$run_json")" = "$RELEASE_BUILD_NUMBER"',
        '"ios_build / ios_build"',
        'test "$source_version" = "$RELEASE_VERSION"',
        "environment: release",
        "APP_STORE_API_KEY: ${{ secrets.APP_STORE_API_KEY }}",
        "APP_STORE_KEY_ID: ${{ secrets.APP_STORE_KEY_ID }}",
        "APP_STORE_ISSUER_ID: ${{ secrets.APP_STORE_ISSUER_ID }}",
    ):
        if expected not in app_store:
            violations.append(
                f"submit_app_store.yml: missing protected submission control: {expected}"
            )

    fdroid_repair = (WORKFLOWS / "repair_fdroid_release.yml").read_text(encoding="utf-8")
    if PR_TRIGGER.search(fdroid_repair):
        violations.append(
            "repair_fdroid_release.yml: protected repair must not run on pull_request"
        )
    for expected in (
        "workflow_dispatch:",
        "contents: write",
        'test "$GITHUB_REF" = "refs/heads/main"',
        'test "$GITHUB_SHA" = "$current_main"',
        'test "$REPLACE_CONFIRMATION" = "replace-v$RELEASE_VERSION"',
        'test "$tag_sha" = "$RELEASE_SOURCE_SHA"',
        "source_sha: ${{ needs.preflight.outputs.source_sha }}",
        "legacy_android_version_code: ${{ needs.preflight.outputs.android_version_code }}",
        "environment: release",
        "--clobber",
        "versionCode=$ANDROID_VERSION_CODE",
    ):
        if expected not in fdroid_repair:
            violations.append(
                f"repair_fdroid_release.yml: missing fail-closed repair control: {expected}"
            )

    fastfile = (ROOT.parent / "fastlane" / "Fastfile").read_text(encoding="utf-8")
    for expected in (
        'lane :submit_app_store_review do',
        'ENV.fetch("APP_STORE_VERSION")',
        'ENV.fetch("APP_STORE_BUILD_NUMBER")',
        "build_number: selected_build",
        "skip_binary_upload: true",
        "submit_for_review: true",
        "automatic_release: true",
    ):
        if expected not in fastfile:
            violations.append(
                f"fastlane/Fastfile: missing exact production submission control: {expected}"
            )
    upload_testflight_lane = fastfile.split("lane :upload_testflight do", 1)[-1].split("\n  end", 1)[0]
    for expected in (
        "skip_waiting_for_build_processing: true",
        "distribute_external: false",
        "notify_external_testers: false",
    ):
        if expected not in upload_testflight_lane:
            violations.append(
                f"fastlane/Fastfile: missing internal TestFlight upload control: {expected}"
            )
    if "changelog:" in upload_testflight_lane:
        violations.append(
            "fastlane/Fastfile: internal TestFlight upload must not wait to patch changelog metadata"
        )

    torturer = (WORKFLOWS / "torturer.yml").read_text(encoding="utf-8")
    if "pull_request_target" in torturer:
        violations.append("torturer.yml: pull_request_target is forbidden for untrusted candidate execution")
    if not PR_TRIGGER.search(torturer):
        violations.append("torturer.yml: public verification must run on pull_request")
    if SECRET.search(torturer) or "environment:" in torturer:
        violations.append("torturer.yml: public verification must not consume secrets or environments")
    if not re.search(
        rf"uses:\s+DobbyVPN/Torturer/\.github/workflows/verify\.yml@{FULL_SHA}\s*$",
        torturer,
        re.MULTILINE,
    ):
        violations.append("torturer.yml: reusable Torturer workflow must be pinned to a full commit SHA")
    for expected_input in (
        "source_repository: ${{ github.event.pull_request.head.repo.full_name }}",
        "commit_sha: ${{ github.event.pull_request.head.sha }}",
        "pr_number: ${{ format('{0}', github.event.pull_request.number) }}",
    ):
        if expected_input not in torturer:
            violations.append(f"torturer.yml: missing immutable candidate input: {expected_input}")
    if not re.search(r"^permissions:\s*\n\s{2}contents:\s*read\s*$", torturer, re.MULTILINE):
        violations.append("torturer.yml: top-level permissions must be contents: read only")
    for legacy in (
        WORKFLOWS / "desktop_cli_tests.yml",
        ROOT / "scripts" / "prepare_cli_test_config.py",
    ):
        if legacy.exists():
            violations.append(
                f"{legacy.relative_to(ROOT)}: private real-profile orchestration must not live in public source"
            )
    if "desktop_cli_tests" in release or "DOBBYVPN_CLI_TEST_CONFIG" in release:
        violations.append("release.yml: legacy private desktop profile workflow reference is forbidden")

    for name in ("android_build.yml", "desktop_build.yml", "ios_build.yml"):
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
