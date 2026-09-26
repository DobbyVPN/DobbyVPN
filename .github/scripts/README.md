# Build and release scripts

These scripts build the shared Go backend and CLI, native desktop frontends,
mobile runtimes, and platform packages. They are used locally and by GitHub
Actions.

The Go toolchain is pinned in .go-version. Local tools downloaded by the
desktop build helper are kept in .local-tools/desktop-build.

## Desktop commands

Build the Go backend and CLI for the current host:

    python3 .github/scripts/desktop_build.py libs --with-cli

Build the Windows or macOS native frontend on its target operating system:

    python3 .github/scripts/desktop_build.py native-ui --platform current --output <path>

The Windows frontend is built with dotnet and the macOS frontend with Swift
Package Manager. Linux has no graphical frontend build.

Stage desktop backends, CLIs, and available native frontend inputs:

    python3 .github/scripts/desktop_build.py app --skip-libs

Assemble Windows and macOS archives and the Linux DEB from staged inputs:

    python3 .github/scripts/package_desktop.py --version 1.5.1 --output output

The Windows and macOS installers consume those archives. The Linux package
contains the backend, CLI, and service files. Desktop packaging does not install
a JVM.

The Windows backend package includes dobbyvpn-backend.exe, dobby_bridge.dll,
and wintun.dll. macOS packages include the backend and CLI in the app bundle;
the Intel package also includes the pinned TrustTunnel helper.

## Android

Android uses Kotlin and Jetpack Compose for its UI and a Go backend library for
VPN behavior. Build commands require JDK 17, Android SDK, and the pinned Go and
NDK toolchains. The main release build is:

    cd android_module
    ./gradlew -PdobbyGoBinary="$(go env GOROOT)/bin/go" :app:assembleRelease

The build packages the Go backend and TrustTunnel's native bridge for both
arm64-v8a and x86_64. CI checks the bridge symbols in both APK ABIs and rejects
unresolved C++ runtime imports, including the symbol implicated in the 1.5.0
Android startup crash. A separate hosted ARM64 job runs the selected Go runtime
tests on a native ARM64 Linux runner.

The local Harness builds the app and its Android instrumentation tests from the
same selected worktree. Android mini runs on an emulator and checks rendered
Compose controls and the VPN service.

## iOS

The iOS application uses SwiftUI and embeds the Go NetworkExtension runtime.
The Test workflow builds the Go NetworkExtension runtime with the TrustTunnel
Simulator bridge, then builds the Simulator app and runs the XCTest UI contract.
This checks native linking and UI rendering; it does not claim physical-device
VPN traffic. That requires a physical iOS runner.

The release workflow builds and signs the physical-device package with the
configured Apple certificates and provisioning profiles. Swift lifecycle
tests can be run with:

    swift test --enable-code-coverage --package-path swift_module

## Functional qualification

The supported checks and platform coverage are documented in [TESTING.md](../../TESTING.md)
and [the functional contract](../../torturer/docs/contract.md).

Hosted Release qualification installs the exact packages built in that run and
runs the canonical mini suite. The private owner Harness also supports local
mini checks. Windows and macOS full checks require interactive desktop guests
and exercise the native frontend with the installed Go backend. Linux checks
cover the backend and CLI only.

The private Harness builds candidate packages from a selected worktree and
runs the functional engine from that same worktree. Its run descriptor records
the app/package paths and platform details needed by the guest adapter.

## Android reproducibility and F-Droid

Release verifies Android APK reproducibility and source identity. The F-Droid
check updates a temporary metadata candidate, then uses fdroidserver's build
server to build the same Kotlin/Compose app and Go backend from the candidate
source. It compares the resulting unsigned APK with the signed Release APK's
payload.

The Android recipe pins the Go toolchain and Compose dependencies used by the
product build. Historical changelogs remain as published release records.

## iOS signing and release metadata

The iOS signing check validates app and extension signatures, provisioning
profiles, bundle identifiers, App Group, source revision, version, build
number, and packet-tunnel entitlement before uploading the package as a
run-scoped artifact.

Release provenance records asset checksums and source metadata. It contains no
credentials or private test evidence.
