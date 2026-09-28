# Testing DobbyVPN

Product and functional tests live in this repository and are built from the
same revision. Run relevant checks for each change. Missing tools and
unavailable platforms are reported as unavailable, never as a pass.

The shared source-check commands are in
[`source_checks.py`](.github/scripts/source_checks.py). CI calls the same
commands used by the local Harness. Tool setup belongs to those commands or
the platform runner setup; downloaded lint and scan binaries use one temporary
directory and are removed after each command. GitHub YAML selects runners,
sets up Android/Xcode SDKs where needed, and calls the commands.

The supported platform coverage and canonical functional scenarios are
defined in the [functional contract](torturer/docs/contract.md). In brief,
mini is portable; full is currently available only on Windows and macOS with
an interactive desktop. Android and iOS physical-device extensions are
deferred. Linux GUI work is outside the current scope.

## Shared source checks

Run a named check from the repository root with:

    python3 .github/scripts/source_checks.py <check>

Supported checks are `go-tests`, `go-unit`, `go-race`,
`go-native-runtime`, `swift-unit`, `lint-go`, `lint-android`, `lint-swift`,
`security`, and `actionlint`. `go-tests` runs unit and race checks with one
dependency setup; the separate commands are available for focused runs.
`security` runs Trivy on Go and Android sources and TruffleHog over Git history.
If source was copied without `.git`, pass `--git-repository <checkout>` so
TruffleHog can inspect the product history.

Coverage checks accept `--output-dir <path>` to retain their files. Without
it, GitHub Actions writes coverage to its runner temporary directory and local
runs remove their temporary coverage directory after reporting.

CI runs Go tests, selected native Go runtime tests, Swift tests, Go/Android/
Swift lint, dependency and credential scans, Actionlint, Android build checks,
and the iOS Simulator UI check. The Android build and iOS Simulator jobs remain
in the platform-check workflow until the shared platform commands own them.

## Go

`go-tests` runs the Go unit suite and targeted race suite on Linux. It prepares
the pinned Go toolchain and Linux TrustTunnel/C++ runtime in the same process
that runs the tests, so native linker settings reach each Go command.

    python3 .github/scripts/source_checks.py go-tests

For focused runs, use `go-unit` or `go-race`. `go-native-runtime` runs selected
packages with the host's Go toolchain and race detector on Linux, Windows, and
macOS. CI runs those same packages on ARM64 Linux, Windows, and macOS. The Linux
ARM64 job checks its runner architecture explicitly. The local Linux runner
does not provide ARM64 hardware.

## Android

The Android app is built from android_module. It uses Kotlin and Jetpack
Compose for the frontend, with a thin Android VPN service boundary and the
shared Go backend. Build the release app and instrumentation test companion
with JDK 17, Android SDK, and the pinned NDK:

    cd android_module
    ./gradlew -PdobbyGoBinary="$(go env GOROOT)/bin/go" :app:assembleReleaseAndroidTest :app:assembleRelease

There are currently no Android app unit-test sources and no Android unit-test
task. The platform check builds the instrumentation companion; Android mini
executes the on-device functional tests.

The platform check builds both APK ABIs and verifies the TrustTunnel bridge
symbols and C++ runtime links. `lint-android` uses JDK 17, Android SDK platform
35, build-tools 36.0.0, and NDK 28.1.13356709. Physical-device Android VPN
qualification remains deferred.

## iOS

`swift-unit` runs the Swift lifecycle tests and reports coverage on macOS:

    python3 .github/scripts/source_checks.py swift-unit

The platform check builds the Go NetworkExtension runtime for iOS Simulator
with the TrustTunnel bridge, packages the SwiftUI Simulator app, and runs its
XCTest UI check. This verifies Go/native linking and app rendering; it does not
claim physical-device VPN traffic. Physical-device full coverage requires a
device runner.
During local `--complete`, after the iOS Simulator check, the Mac runs
`.github/scripts/ios_production_check.py`. It uses the same
`package_ios_app.sh iosanalyze` and `iosarchive` steps as Release, and derives a
deterministic build number from `VERSION`.

## Desktop

Windows and macOS native frontends must be built on their respective target
hosts. Build commands and package inputs are documented in
[the desktop build guide](.github/scripts/README.md). Linux desktop checks
remain backend and CLI only.

The private owner Harness runs local mini checks and the interactive Windows
or macOS full journey on disposable guests. Its commands and guest setup are
documented in the private owner workspace. Hosted Release qualification uses the
exact packages built in that run and runs the canonical hosted mini suite.
The owner workspace's complete qualification first requires local Linux mini,
Windows full, Android mini, macOS full, and iOS Simulator mini on one product
revision, then CI and the non-publishing Release at that revision. Every
discovered profile in each supplied local configuration must pass. A protocol
absent from a supplied configuration remains untested. The hosted Release uses
an Outline-only profile, so its pass does not replace a failed local Xray or
TrustTunnel result.

The local complete run also invokes the shared source-check commands on the
platforms that need them: Go unit, race, runtime, and lint checks on Linux;
runtime checks on Windows; Android Lint on Android; and Go runtime, Swift test,
and Swift lint checks on macOS. The controller runs dependency/credential
scans and workflow validation. Windows and macOS build and install their
desktop packages and run installer migration checks before the full journey.

For execution-time analysis, the owner complete result records UTC boundaries
and elapsed time for each local lane and controller phase, plus Actions job and
step timestamps. Local candidate and hosted functional logs record UTC progress
events and monotonic phase or command durations. These measurements do not
change the functional pass criteria in the contract.

`lint-go`, `lint-android`, and `lint-swift` run the configured language checks.
`security` scans Go and Android dependencies for high/critical fixed
vulnerabilities and scans Git history for detected secrets. `actionlint`
validates every GitHub workflow file. These commands download pinned scanner
and linter binaries into a temporary directory and remove them afterward.

Release is dispatched from main and qualifies packages; it does not publish.
Publication is a separate manual workflow and requires an explicit owner
request.
