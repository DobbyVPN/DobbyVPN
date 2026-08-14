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
STEP_NAME = re.compile(
    r"^\s*-\s+name:\s*(?:"
    r'"(?P<double>[^"]+)"|'
    r"'(?P<single>[^']+)'|"
    r"(?P<plain>[^#]+?))\s*$"
)


def _workflow_step_lines(source: str) -> dict[str, list[int]]:
    steps: dict[str, list[int]] = {}
    for line_number, line in enumerate(source.splitlines(), start=1):
        match = STEP_NAME.match(line)
        if match is None:
            continue
        name = next(
            value for value in match.groupdict().values() if value is not None
        ).strip()
        steps.setdefault(name, []).append(line_number)
    return steps


def _step_order_violation(source: str, before: str, after: str) -> str | None:
    steps = _workflow_step_lines(source)
    for name in (before, after):
        if len(steps.get(name, ())) != 1:
            return f"expected exactly one actual workflow step named {name!r}"
    if steps[before][0] > steps[after][0]:
        return f"workflow step {before!r} must run before {after!r}"
    return None


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

    # GitHub retired the Node runtimes used by these major versions. Keep the
    # official action-major policy explicit so a copied legacy step cannot
    # silently reintroduce the warning (or depend on GitHub's compatibility shim).
    for action_file in sorted((ROOT / "actions").rglob("*.yml")) + sorted(WORKFLOWS.glob("*.yml")):
        action_text = action_file.read_text(encoding="utf-8")
        relative = action_file.relative_to(ROOT)
        for legacy, replacement in (
            ("actions/checkout@v4", "actions/checkout@v5"),
            ("actions/download-artifact@v4", "actions/download-artifact@v7"),
            ("actions/download-artifact@v5", "actions/download-artifact@v7"),
            ("actions/download-artifact@v6", "actions/download-artifact@v7"),
            ("actions/upload-artifact@v4", "actions/upload-artifact@v7"),
            ("actions/upload-artifact@v5", "actions/upload-artifact@v7"),
            ("actions/upload-artifact@v6", "actions/upload-artifact@v7"),
            ("actions/cache@v4", "actions/cache@v5"),
            ("actions/setup-go@v5", "actions/setup-go@v6"),
            ("actions/setup-java@v4", "actions/setup-java@v5"),
            ("android-actions/setup-android@v3", "android-actions/setup-android@v4"),
            ("peter-evans/create-pull-request@v7", "peter-evans/create-pull-request@v8"),
            ("gradle/actions/setup-gradle@06832c7b30a0129d7fb559bcc6e43d26f6374244", "gradle/actions/setup-gradle@v5"),
            ("microsoft/setup-msbuild@v1.1", "microsoft/setup-msbuild@v3"),
            ("microsoft/setup-msbuild@v2", "microsoft/setup-msbuild@v3"),
        ):
            if legacy in action_text:
                violations.append(f"{relative}: use {replacement}; {legacy} uses a retired Node runtime")
    release = (WORKFLOWS / "release.yml").read_text(encoding="utf-8")
    if PR_TRIGGER.search(release):
        violations.append("release.yml: protected release workflow must not run on pull_request")
    if release.count("source_sha: ${{ github.sha }}") != 6:
        violations.append("release.yml: every artifact-producing reusable workflow must receive github.sha")
    for expected in (
        "matching-refs/tags/$release_tag",
        '[[ "$object_sha" != "$SOURCE_SHA" ]]',
        "ref: ${{ github.sha }}",
        'test "$GITHUB_REF" = "refs/heads/main"',
        'test "$SOURCE_SHA" = "$current_main"',
        'test "$(git rev-parse HEAD)" = "$SOURCE_SHA"',
    ):
        if expected not in release:
            violations.append(f"release.yml: missing exact source/tag guard: {expected}")

    for name in (
        "android_build.yml",
        "desktop_libs_generate.yml",
        "desktop_build.yml",
        "installers_build.yml",
        "ios_libs_generate.yml",
        "ios_build.yml",
    ):
        source = (WORKFLOWS / name).read_text(encoding="utf-8")
        for expected in (
            "source_sha:",
            "required: true",
            "ref: ${{ inputs.source_sha }}",
            "^[0-9a-f]{40}$",
            "git rev-parse HEAD",
        ):
            if expected not in source:
                violations.append(f"{name}: missing exact source checkout control: {expected}")
        if re.search(r'test\s+"\$[A-Z_]*SOURCE_SHA"\s+=~', source):
            violations.append(f"{name}: Bash regex validation must use [[ ... =~ ... ]], not test")

    android_build = (WORKFLOWS / "android_build.yml").read_text(encoding="utf-8")
    for expected in (
        "APP_SOURCE_SHA: ${{ inputs.source_sha }}",
        "APP_SOURCE_REPOSITORY: ${{ github.repository }}",
        "FDROID_COMPAT_SOURCE_ROOT: /home/vagrant/build/com.dobby.vpn",
        "FDROID_COMPAT_GO_ROOT: /home/vagrant/build/srclib/go",
        "GOPATH: /home/vagrant/go",
        "GOMOBILE: /home/vagrant/go/bin/gomobile",
        "GOFLAGS: -trimpath -buildvcs=false",
        "EXPECTED_ANDROID_SIGNER_SHA256: c3f0414a74012060d7c6aa3a3d9dac0aa13c1bd23b7512eefd860fb865e67933",
        "ndk;27.3.13750724",
        'GO_SRC="$FDROID_COMPAT_GO_ROOT"',
        'ANDROID_GO_VERSION: ${{ steps.android_go.outputs.version }}',
        'GO_VERSION="$ANDROID_GO_VERSION"',
        'GO_COMMIT="56ebf80e57db9f61981fc0636fc6419dc6f68eda"',
        'git -C "$GO_SRC" rev-parse HEAD',
        "Check out trusted APK source verifier",
        "ref: ${{ github.workflow_sha }}",
        "path: .trusted-workflow",
        'python3 "$GITHUB_WORKSPACE/.trusted-workflow/.github/scripts/verify_android_apk_source.py"',
        ".github/scripts/verify_android_reproducibility.py",
        "Verify reproducible Android toolchain",
        "Build first isolated unsigned APK",
        "Remove first-build outputs",
        "Build second isolated unsigned APK",
        "Verify complete unsigned APK reproducibility",
        "--no-build-cache --no-daemon --rerun-tasks",
        ":app:assembleRelease",
        "DOBBYVPN_GOMOBILE_GOCACHE:",
        "DOBBYVPN_GOMOBILE_GOTMPDIR:",
        "dobbyvpn-android-repro/first.apk",
        "android-reproducibility.json",
        "golang.org/x/mobile/cmd/gobind@v0.0.0-20260520154334-0e4426e1883d",
        "Sign the verified unsigned APK",
        "apksigner\" sign",
        "Verify signed APK payload binding",
        "verify-signed-payload",
        "certificate SHA-256 digest",
        '"signer_certificate_sha256": os.environ["EXPECTED_ANDROID_SIGNER_SHA256"]',
        '"signed_payload_matches_unsigned": True',
        '"reproducibility": json.loads(',
        '--source-sha "$SOURCE_SHA" --repository "$APP_SOURCE_REPOSITORY"',
    ):
        if expected not in android_build:
            violations.append(
                f"android_build.yml: missing embedded tagged-source control: {expected}"
            )
    if "Cache gomobile Android AAR" in android_build or "gomobile-aar-" in android_build:
        violations.append(
            "android_build.yml: final gomobile AAR caching is forbidden because toolchain and environment paths affect native bytes"
        )
    for forbidden_cache in ("Cache Android Go toolchain", "Cache gomobile"):
        if forbidden_cache in android_build:
            violations.append(
                f"android_build.yml: {forbidden_cache} is forbidden for reproducible release inputs"
            )
    step_order_violation = _step_order_violation(
        android_build,
        "Prepare F-Droid-compatible tool paths",
        "Set up bootstrap Go",
    )
    if step_order_violation is not None:
        violations.append(f"android_build.yml: {step_order_violation}")
    if "GITHUB_SHA: ${{ inputs.source_sha }}" in android_build:
        violations.append(
            "android_build.yml: a job-level assignment cannot override GitHub's reserved GITHUB_SHA"
        )
    if '"$GOPATH/bin/gomobile" init' in android_build:
        violations.append(
            "android_build.yml: gomobile init is forbidden because it resolves an unpinned gobind"
        )
    if 'GO_VERSION="${{ steps.android_go.outputs.version }}"' in android_build:
        violations.append(
            "android_build.yml: Go version output must enter shell through the step environment"
        )

    test_workflow = (WORKFLOWS / "test.yml").read_text(encoding="utf-8")
    if '"$HOME/go/bin/gomobile" init' in test_workflow:
        violations.append(
            "test.yml: gomobile init is forbidden because it resolves an unpinned gobind"
        )

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
        'test "$GITHUB_REF" = "refs/heads/main"',
        'test "$GITHUB_SHA" = "$current_main"',
        'test "$RELEASE_SOURCE_SHA" = "$current_main"',
        'test "$(git rev-parse HEAD)" = "$RELEASE_SOURCE_SHA"',
        'test "$(jq -r .conclusion <<<"$run_json")" = "success"',
        'test "$(jq -r .headSha <<<"$run_json")" = "$RELEASE_SOURCE_SHA"',
        'test "$source_version" = "$RELEASE_VERSION"',
        "run-id: ${{ inputs.run_id }}",
        "QUALIFIED_LINUX_SHA256: ${{ inputs.linux_sha256 }}",
        "QUALIFIED_WINDOWS_AMD64_SHA256: ${{ inputs.windows_amd64_sha256 }}",
        "QUALIFIED_MACOS_AMD64_SHA256: ${{ inputs.macos_amd64_sha256 }}",
        "Verify locally qualified desktop packages",
        'matching-refs/tags/$tag',
        'gh release create "$release_tag"',
        "--draft",
        "--verify-tag",
        'verify_remote_tag "$expected_tag_object"',
        "draft_created=false",
        "owned_release_id=\"\"",
        "wait_for_exact_release()",
        "for _ in {1..30}",
        'release_record="$(wait_for_exact_release true)"',
        'release_record="$(wait_for_exact_release false)"',
        'gh api "repos/$GITHUB_REPOSITORY/releases/$owned_release_id"',
        "release_provenance.py create",
        "release_provenance.py verify",
        "cmp release/release-provenance.json published/release-provenance.json",
        "published=true",
        "dobbyvpn-android-provenance",
        "Android provenance validation passed",
        "verify_android_apk_source.py",
        "verify_android_reproducibility.py verify-provenance",
        "verify_android_reproducibility.py verify-signed-payload",
        "Android signed-payload binding is missing",
        "Android signer certificate mismatch",
    ):
        if expected not in promotion:
            violations.append(
                f"promote_release.yml: missing fail-closed promotion control: {expected}"
            )

    gradle_build = (ROOT.parent / "kmp_module" / "app" / "build.gradle.kts").read_text(
        encoding="utf-8"
    )
    for expected in (
        'environmentVariable("DOBBYVPN_GOMOBILE_GOCACHE")',
        'environmentVariable("DOBBYVPN_GOMOBILE_GOTMPDIR")',
        'inheritedGoFlags + listOf("-trimpath", "-buildvcs=false")',
        'flag.startsWith("-trimpath=")',
        'flag.startsWith("-buildvcs=")',
        ').distinct().joinToString(" ")',
    ):
        if expected not in gradle_build:
            violations.append(
                f"build.gradle.kts: missing Android reproducibility control: {expected}"
            )

    # A retry against an already-published tag must still fetch the exact
    # selected Actions-run packages and compare them with the locally tested
    # digests.  Otherwise a same-source but differently packaged run could be
    # accepted merely because an older release asset happened to match.
    for step in (
        "Download Linux package",
        "Download Windows amd64 package",
        "Download macOS amd64 package",
        "Download macOS arm64 package",
        "Download signed Android package",
        "Download unsigned Android package",
        "Download Android provenance",
        "Verify locally qualified desktop packages",
        "Verify Android source and artifact provenance",
        "Create F-Droid version metadata",
        "Create and verify public release provenance",
    ):
        marker = f"      - name: {step}"
        start = promotion.find(marker)
        end = promotion.find("\n      - name:", start + len(marker)) if start >= 0 else -1
        section = promotion[start:] if end < 0 else promotion[start:end]
        if start < 0 or re.search(r"^\s*if:\s*", section, re.MULTILINE):
            violations.append(
                f"promote_release.yml: {step} must run for a published-release retry"
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
        'test "$RELEASE_SOURCE_SHA" = "$current_main"',
        'test "$(jq -r .conclusion <<<"$run_json")" = "success"',
        'test "$(jq -r .headSha <<<"$run_json")" = "$RELEASE_SOURCE_SHA"',
        'test "$(jq -r .number <<<"$run_json")" = "$RELEASE_BUILD_NUMBER"',
        '"ios_build / ios_build"',
        'test "$source_version" = "$RELEASE_VERSION"',
        "DobbyVPN.ipa.provenance",
        "ios_artifact_provenance.py verify",
        "APP_STORE_SOURCE_SHA: ${{ inputs.commit_sha }}",
        "run-id: ${{ inputs.run_id }}",
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
        'release-provenance.json',
        "manifest-bearing releases are immutable",
        "Download rebuilt Android provenance",
        'echo "provenance=$provenance"',
        "Replace only F-Droid-facing Android assets",
        "Confirm repaired public metadata",
        'gh release download "$RELEASE_TAG"',
        'cmp "${{ steps.apk.outputs.signed }}" "published/DobbyVPN-v$RELEASE_VERSION-sign.apk"',
        'cmp "${{ steps.apk.outputs.unsigned }}" "published/DobbyVPN-v$RELEASE_VERSION-unsign.apk"',
        'cmp "${{ steps.apk.outputs.provenance }}" "published/DobbyVPN-v$RELEASE_VERSION-android-provenance.json"',
        "manifest-bearing releases are immutable; refusing legacy Android asset repair",
        "environment: release",
        "--clobber",
        "versionCode=$ANDROID_VERSION_CODE",
        "verify_android_apk_source.py",
        "verify_android_reproducibility.py verify-provenance",
        "verify_android_reproducibility.py verify-signed-payload",
        "Android signer certificate mismatch",
    ):
        if expected not in fdroid_repair:
            violations.append(
                f"repair_fdroid_release.yml: missing fail-closed repair control: {expected}"
            )

    fastfile = (ROOT.parent / "fastlane" / "Fastfile").read_text(encoding="utf-8")
    for expected in (
        'COMMIT          = ENV["APP_SOURCE_SHA"] || ENV["GITHUB_SHA"]',
        'REPO            = ENV["APP_SOURCE_REPOSITORY"] || ENV["GITHUB_REPOSITORY"]',
        'lane :submit_app_store_review do',
        'ENV.fetch("APP_STORE_VERSION")',
        'ENV.fetch("APP_STORE_BUILD_NUMBER")',
        "build_number: selected_build",
        'copyright: "#{Time.now.year} DobbyVPN contributors"',
        '"en-US" => "https://github.com/DobbyVPN/DobbyVPN/issues"',
        '"en-US" => "https://dobbyvpn.com/privacy"',
        "skip_binary_upload: true",
        "submit_for_review: true",
        "automatic_release: true",
        "rescue Spaceship::UnexpectedResponse",
        "specified pre-release build could not be added",
        "sleep(240)",
    ):
        if expected not in fastfile:
            violations.append(
                f"fastlane/Fastfile: missing exact production submission control: {expected}"
            )
    upload_testflight_lane = fastfile.split("lane :upload_testflight do", 1)[-1].split("\n  end", 1)[0]
    for expected in (
        'ipa: "#{EXPORT_DIR}/#{IPA_NAME}"',
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

    for name in ("ios_build.yml", "submit_app_store.yml"):
        text = (WORKFLOWS / name).read_text(encoding="utf-8")
        ruby_version = re.search(
            r"ruby-version:\s*['\"]?(\d+)\.(\d+)(?:\.\d+)?['\"]?(?![\d.])",
            text,
        )
        if ruby_version is None or tuple(map(int, ruby_version.groups())) < (3, 3):
            violations.append(f"{name}: protected Fastlane job must use supported Ruby 3.3+")

    ios_build = (WORKFLOWS / "ios_build.yml").read_text(encoding="utf-8")
    for expected in (
        "ios_artifact_provenance.py create",
        "Verify signed IPA App Group and packet tunnel entitlements",
        "verify_ios_app_group.py",
        'codesign --display --entitlements :- "${apps[0]}"',
        'security cms -D -i "${tunnels[0]}/embedded.mobileprovision"',
        "name: DobbyVPN.ipa.provenance",
        "retention-days: 90",
        "- name: Fastlane upload_testflight",
        "if: ${{ github.ref == 'refs/heads/main' }}",
        "bundle exec fastlane ios upload_testflight",
    ):
        if expected not in ios_build:
            violations.append(f"ios_build.yml: missing IPA provenance control: {expected}")
    ipa_upload = ios_build.find("      - name: Upload IPA as artifact")
    provenance_upload = ios_build.find("      - name: Upload IPA provenance as artifact")
    testflight_upload = ios_build.find("      - name: Fastlane upload_testflight")
    if min(ipa_upload, provenance_upload, testflight_upload) < 0 or not (
        ipa_upload < testflight_upload and provenance_upload < testflight_upload
    ):
        violations.append(
            "ios_build.yml: TestFlight upload must run only after the IPA and its provenance are retained"
        )

    ios_libraries = (WORKFLOWS / "ios_libs_generate.yml").read_text(encoding="utf-8")
    for expected in (
        "APP_SOURCE_SHA: ${{ inputs.source_sha }}",
        "APP_SOURCE_REPOSITORY: ${{ github.repository }}",
        'test "$APP_SOURCE_SHA" = "$(git -C "$GITHUB_WORKSPACE" rev-parse HEAD)"',
        '-PprojectRepositoryCommit="${APP_SOURCE_SHA}"',
        '-PprojectRepositoryCommitLink="https://github.com/${APP_SOURCE_REPOSITORY}/tree/${APP_SOURCE_SHA}"',
    ):
        if expected not in ios_libraries:
            violations.append(f"ios_libs_generate.yml: missing exact KMP framework-source control: {expected}")
    for forbidden in (
        "GITHUB_SHA: ${{ inputs.source_sha }}",
        "Cache KMP iOS release framework",
        "steps.kmp_ios_framework_cache.outputs.cache-hit",
    ):
        if forbidden in ios_libraries:
            violations.append(
                f"ios_libs_generate.yml: stale final-framework cache/source override is forbidden: {forbidden}"
            )
    for artifact_name in ("MyLibrary.xcframework", "app.framework"):
        marker = f"          name: {artifact_name}"
        start = ios_libraries.find(marker)
        end = ios_libraries.find("\n      - name:", start + len(marker)) if start >= 0 else -1
        section = ios_libraries[start:] if end < 0 else ios_libraries[start:end]
        if start < 0 or "if-no-files-found: error" not in section:
            violations.append(
                f"ios_libs_generate.yml: {artifact_name} producer upload must fail when output is missing"
            )

    lint = (WORKFLOWS / "lint.yml").read_text(encoding="utf-8")
    if "reviewdog/action-golangci-lint@c76cceaaab89abe74e649d2e34c6c9adc26662d2" not in lint:
        violations.append("lint.yml: golangci-lint review action must use its Node 24 SHA pin")
    for expected in (
        "if-no-files-found: error",
        "mapfile -d '' -t detekt_files",
        "find kmp_module -path \"*/build/reports/detekt/*.xml\" -type f -print0 | sort -z",
        "-fail-level=error",
    ):
        if expected not in lint:
            violations.append(f"lint.yml: missing fail-closed Detekt reporting control: {expected}")

    # None of these workflows pushes commits or tags. Avoid retaining the
    # checkout token in their Git configuration after source retrieval.
    for name in (
        "actionlint.yml",
        "android_build.yml",
        "desktop_build.yml",
        "desktop_libs_generate.yml",
        "fdroid_scan_apk.yml",
        "installers_build.yml",
        "ios_build.yml",
        "lint.yml",
        "repair_fdroid_release.yml",
        "security.yml",
        "test.yml",
    ):
        text = (WORKFLOWS / name).read_text(encoding="utf-8")
        checkouts = text.count("uses: actions/checkout@v5")
        persisted = text.count("persist-credentials: false")
        if persisted < checkouts:
            violations.append(f"{name}: every checkout must disable persisted credentials")

    for name in (
        "android_build.yml",
        "desktop_build.yml",
        "desktop_libs_generate.yml",
        "installers_build.yml",
        "ios_build.yml",
    ):
        text = (WORKFLOWS / name).read_text(encoding="utf-8")
        uploads = text.count("uses: actions/upload-artifact@v7")
        required = text.count("if-no-files-found: error")
        if required < uploads:
            violations.append(f"{name}: every release artifact upload must fail when its output is missing")

    for expected in (
        "python3 -m unittest discover -s .github/scripts -p 'test_*.py'",
        "set +e\n          python3 .github/scripts/check_swift_coverage.py",
        'cat "$COVERAGE_SUMMARY" >> "$GITHUB_STEP_SUMMARY"',
        'exit "$coverage_status"',
        "${{ runner.temp }}/swift-lifecycle-core.lcov",
        "${{ runner.temp }}/swift-lifecycle-core-coverage.md",
        "if-no-files-found: error",
    ):
        if expected not in test_workflow:
            violations.append(f"test.yml: missing Swift coverage evidence control: {expected}")

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
    if "go-go-tunnel/releases/download/" in desktop_libs:
        violations.append(
            "desktop_libs_generate.yml: go-go-tunnel downloads must use desktop_build.py's single pinned release table"
        )

    installers = (WORKFLOWS / "installers_build.yml").read_text(encoding="utf-8")
    for arch in ("arm64", "amd64"):
        expected = f"name: macos_grpcvpnserver-{arch}"
        destination = f"path: installer/macos/services/{arch}"
        if expected not in installers or destination not in installers:
            violations.append(
                f"installers_build.yml: macOS {arch} service must be downloaded into its architecture-specific path"
            )

    # The Windows desktop archive, VPN service, and bridge are all amd64
    # binaries. Publishing x86 or arm64 MSI wrappers around those payloads
    # would create installers that cannot run on their advertised targets.
    windows_installer = (ROOT.parent / "installer" / "windows" / "build.bat").read_text(
        encoding="utf-8"
    )
    desktop_build = (ROOT / "scripts" / "desktop_build.py").read_text(encoding="utf-8")
    for unsupported in ("dobbyVPN-windows-x86.msi", "dobbyVPN-windows-arm64.msi"):
        if unsupported in installers or unsupported in promotion:
            violations.append(
                f"Windows installer contract: {unsupported} must not be published without matching native payloads"
            )
    for expected in (
        "call :msi amd64 x64",
        '-d "DOBBYVPN_PLATFORM=%1"',
        "cmd /c exit 1\n\t\t\tgoto :error",
        ":error\n\techo [-] Failed.\n\texit /b 1",
    ):
        if expected not in windows_installer:
            violations.append(
                f"installer/windows/build.bat: missing fail-closed amd64 installer control: {expected}"
            )
    for runtime_file in (
        "windows_grpcvpnserver.exe",
        "dobby_bridge.dll",
        "wintun.dll",
    ):
        if desktop_libs.count(f"go_module/{runtime_file}") != 2:
            violations.append(
                "desktop_libs_generate.yml: Windows runtime closure must be cached and uploaded: "
                + runtime_file
            )
        if runtime_file not in windows_installer:
            violations.append(
                "installer/windows/build.bat: Windows runtime closure must require " + runtime_file
            )
        if f'"{runtime_file}"' not in installers:
            violations.append(
                "installers_build.yml: finished MSI must be checked for " + runtime_file
            )
    if "WINTUN_AMD64_DLL_SHA256" not in desktop_build:
        violations.append("desktop_build.py: Windows Wintun payload must be checksum-pinned")
    for digest in (
        "10e2f921aaa949060bed936e3c361b0967b2ad8b7a71dd983d36abd94c903063",
        "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce",
    ):
        if digest not in desktop_libs:
            violations.append(
                "desktop_libs_generate.yml: cached Windows runtime closure must be checksum-verified"
            )
    if "curl -#fLo wintun.zip" in windows_installer:
        violations.append("installer/windows/build.bat: Wintun must come from the verified artifact")
    for expected in (
        "Verify Windows service runtime closure",
        "Windows service runtime closure is missing a regular $file",
    ):
        if expected not in desktop_libs:
            violations.append(
                "desktop_libs_generate.yml: missing fail-closed Windows runtime control: "
                + expected
            )
    if "SELECT `FileName` FROM `File`" not in installers:
        violations.append("installers_build.yml: finished MSI File table must be verified")
    if violations:
        print("Workflow secret-isolation policy failed:", file=sys.stderr)
        print("\n".join(f"- {item}" for item in violations), file=sys.stderr)
        return 1
    print("Workflow secret-isolation policy passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
