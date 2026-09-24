# Testing DobbyVPN

Product and functional tests live in this repository and are built from the
same revision. Run relevant checks for each change. Missing tools and
unavailable platforms are reported as unavailable, never as a pass.

The supported platform coverage and canonical functional scenarios are
defined in the [functional contract](torturer/docs/contract.md). In brief,
mini is portable; full is currently available only on Windows and macOS with
an interactive desktop. Android and iOS physical-device extensions are
deferred. Linux GUI work is outside the current scope.

## Go

On Linux, the Go tests need the pinned native TrustTunnel bridge and C++
runtime staged in go_module. The Test workflow runs:

    python3 .github/scripts/desktop_build.py prepare-go-test-deps --go-mod-tidy

In an environment with those dependencies available, run from go_module:

    go test -tags=ci ./...
    go test -race ./routing/... ./sessionapi/... ./tunnel/...

The setup command's environment changes do not escape its shell process.

## Android

The Android app is built from android_module. It uses Kotlin and Jetpack
Compose for the frontend, with a thin Android VPN service boundary and the
shared Go backend. Build and run the release unit and instrumentation targets
with JDK 17, Android SDK, and the pinned NDK:

    cd android_module
    ./gradlew -PdobbyGoBinary="$(go env GOROOT)/bin/go" :app:testReleaseUnitTest :app:assembleReleaseAndroidTest :app:assembleRelease

The Test workflow also installs the release-shaped app and instrumentation
APK on its Android emulator and runs the rendered UI and functional checks.
The Go backend is packaged for arm64-v8a and x86_64. TrustTunnel is available
only on arm64-v8a.

## iOS

Run the Swift lifecycle tests on a Mac:

    swift test --enable-code-coverage --package-path swift_module

The Test workflow builds the Go NetworkExtension runtime XCFramework, packages
the SwiftUI Simulator app, and runs its XCTest UI check. This checks app
rendering and lifecycle behavior; it does not claim physical-device VPN
traffic. Physical-device full coverage requires a device runner.

## Desktop

Windows and macOS native frontends must be built on their respective target
hosts. Build commands and package inputs are documented in
[the desktop build guide](.github/scripts/README.md). Linux desktop checks
remain backend and CLI only.

The private owner Harness runs local mini checks and the interactive Windows
or macOS full journey on disposable guests. Its commands and guest setup are
documented in the private owner workspace. Hosted Release qualification uses the
exact packages built in that run and runs the canonical hosted mini suite.

CI runs source, build, lint, and workflow checks. Release is dispatched from
main and qualifies packages; it does not publish. Publication is a separate
manual workflow and requires an explicit owner request.
