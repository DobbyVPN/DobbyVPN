from __future__ import annotations

import re
from pathlib import Path
import subprocess


ROOT = Path(__file__).resolve().parents[2]
DRIVER = ROOT / ".github/scripts/android_build_driver.sh"
CANONICAL_TEST_APK = (
    "kmp_module/app/build/outputs/apk/androidTest/release/"
    "app-release-androidTest.apk"
)
CONSENT_ACTIVITY = "com.dobby.feature.vpn_service.VpnConsentTestActivity"


def test_driver_is_valid_shell_and_keeps_canonical_task_opt_in() -> None:
    subprocess.run(["bash", "-n", str(DRIVER)], check=True)
    source = DRIVER.read_text(encoding="utf-8")
    assert 'DOBBYVPN_BUILD_CANONICAL_INSTRUMENTATION' in source
    assert ":app:assembleReleaseAndroidTest" in source
    assert f"canonical_test_apk_relative='{CANONICAL_TEST_APK}'" in source
    guarded_task = re.search(
        r'if \[\[ "\$canonical_instrumentation" == 1 \]\]; then(?P<body>.*?)\nfi',
        source,
        flags=re.DOTALL,
    )
    assert guarded_task is not None
    assert ":app:assembleReleaseAndroidTest" in guarded_task.group("body")


def test_driver_binds_exact_canonical_descriptor_and_prints_it() -> None:
    source = DRIVER.read_text(encoding="utf-8")
    assert 'document["canonical_test_apk"]' in source
    assert '"path": canonical_descriptor["path"]' in source
    assert '"sha256": canonical_descriptor["sha256"]' in source
    assert '"size_bytes": canonical_descriptor["bytes"]' in source
    assert "canonical_test_apk path=$canonical_test_apk_relative" in source
    assert "sha256=$canonical_test_apk_sha256" in source
    assert "size_bytes=$canonical_test_apk_size_bytes" in source


def test_driver_fails_closed_when_canonical_output_is_missing_or_invalid() -> None:
    source = DRIVER.read_text(encoding="utf-8")
    assert '[[ -f "$canonical_test_apk" && ! -L "$canonical_test_apk" ]]' in source
    assert "Gradle did not produce the canonical release instrumentation APK" in source
    assert 'canonical_test_apk_size_bytes" =~ ^[1-9][0-9]*$' in source


def test_normal_mode_does_not_require_or_describe_test_apk() -> None:
    source = DRIVER.read_text(encoding="utf-8")
    assert 'canonical_instrumentation=0' in source
    assert 'CANONICAL_INSTRUMENTATION="$canonical_instrumentation"' in source
    # The descriptor is added only inside the opt-in branch in the manifest
    # writer; the normal release artifact contract remains unchanged.
    descriptor_branch = re.search(
        r'if canonical_instrumentation:\n(?P<body>.*?)\nencoded =',
        source,
        flags=re.DOTALL,
    )
    assert descriptor_branch is not None
    assert 'document["canonical_test_apk"]' in descriptor_branch.group("body")


def test_release_variant_owns_the_consent_host_and_manifest_entry() -> None:
    android_main_source = (
        ROOT
        / "kmp_module/app/src/androidMain/kotlin/com/dobby/feature/vpn_service"
        / "VpnConsentTestActivity.kt"
    )
    debug_source = (
        ROOT
        / "kmp_module/app/src/androidDebug/kotlin/com/dobby/feature/vpn_service"
        / "VpnConsentTestActivity.kt"
    )
    main_manifest = ROOT / "kmp_module/app/src/androidMain/AndroidManifest.xml"
    debug_manifest = ROOT / "kmp_module/app/src/debug/AndroidManifest.xml"

    assert android_main_source.is_file()
    assert not debug_source.exists()
    assert "class VpnConsentTestActivity" in android_main_source.read_text(encoding="utf-8")
    manifest = main_manifest.read_text(encoding="utf-8")
    assert CONSENT_ACTIVITY in manifest
    assert 'android:exported="false"' in manifest
    assert not debug_manifest.exists()


def test_release_gradle_variant_has_the_instrumentation_runner() -> None:
    gradle = (ROOT / "kmp_module/app/build.gradle.kts").read_text(encoding="utf-8")
    assert "buildTypes" in gradle and "release" in gradle
    assert 'testInstrumentationRunner = "com.dobby.TestApplicationRunner"' in gradle
    instrumentation_manifest = (
        ROOT / "kmp_module/app/src/androidInstrumentedTest/AndroidManifest.xml"
    ).read_text(encoding="utf-8")
    assert "VpnTrafficTestActivity" in instrumentation_manifest
