# Build and release scripts

These scripts build the shared Go backend and CLI, native desktop frontends,
mobile runtimes, and platform packages. They are used locally and by GitHub
Actions.

The Go toolchain is pinned in .go-version. Local tools downloaded by the
desktop build helper are kept in .local-tools/desktop-build.

## Source checks

Use the same source-check commands on local runners and GitHub Actions:

    python3 .github/scripts/source_checks.py <check>

The checks are `go-tests`, `go-unit`, `go-race`, `go-native-runtime`,
`swift-unit`, `lint-go`, `lint-android`, `lint-swift`, `security`,
`actionlint`, and `cache-clean`. Go checks use the Go version in .go-version.
The Go installer and dependency staging run in the same process as tests so
CGO settings are available to the test commands.

Lint, scan, and Actionlint binaries are downloaded at fixed versions, checked
against pinned SHA-256 values, and kept in one temporary directory. That
directory is removed when the command exits. `cache-clean` clears Go build and
test caches plus the golangci-lint cache; it leaves Go module downloads intact.
TruffleHog needs a Git checkout with full history. If the local runner uses a
source archive, pass `--git-repository <controller-checkout>`.

## Desktop commands

Build and package one desktop target on a runner for that target's native OS
and architecture:

    python3 .github/scripts/desktop_platform.py build \
        --platform linux \
        --output-dir output/linux

Use `--platform windows` on Windows or `--platform macos` on macOS. Linux and
Windows packages require amd64. macOS packages use the runner's native
architecture; specify `--arch arm64` or `--arch amd64` only when needed to make
that selection explicit. The command reads the version from `VERSION` and the
source commit from Git. For source archives without `.git`, pass
`--source-sha <full-commit-sha>`; `--version <x.y.z>` can override the version.

The command builds the backend and CLI, builds the Windows or macOS native UI
where applicable, assembles the installer package, and writes the package plus
`desktop-package.json` into the output directory. Local candidate builds and
GitHub desktop package builds use this same command. The resulting package is
a Linux DEB, Windows MSI, or macOS PKG. Linux has no graphical frontend build.

The Windows backend package includes dobbyvpn-backend.exe, dobby_bridge.dll,
and wintun.dll. macOS packages include the backend and CLI in the app bundle;
the Go backend links TrustTunnel's native bridge for both architectures.

## Android

Android uses Kotlin and Jetpack Compose for its UI and a Go backend library for
VPN behavior. Builds require JDK 17, Android SDK, and the pinned Go and NDK
toolchains. The fast build-and-ABI check is
`.github/scripts/android_build_check.sh`; Android PR CI and the Harness's fast
local candidate path both call it. It builds the release app and test
companion once through `android_build_driver.sh --local`, then checks the app's
native libraries for both `arm64-v8a` and `x86_64`. The check verifies TrustTunnel
bridge symbols and rejects unresolved C++ runtime imports, including the
symbol implicated in the 1.5.0 Android startup crash.

Complete local qualification and hosted Release use the driver's Release
mode. That mode builds the app twice with a clean build between runs, checks
APK reproducibility and source/dependency provenance, and verifies the native
ABI payload. Release retains those unsigned outputs for later Publish signing.
The same selected Go runtime tests run on Linux, Windows, and macOS; CI adds a
native Linux ARM64 run.

The local Harness builds the app and its Android instrumentation tests from the
same selected worktree. Android mini runs on an emulator and checks rendered
Compose controls and the VPN service.

## iOS

The iOS application uses SwiftUI and embeds the Go NetworkExtension runtime.
The iOS Simulator job in CI builds the Go NetworkExtension runtime with the
TrustTunnel Simulator bridge, then builds the Simulator app and runs the XCTest
UI contract. The local Harness runs the same contract. During local complete,
the Mac then runs `.github/scripts/ios_production_check.py`, which invokes
`package_ios_app.sh iosanalyze` and `iosarchive` with a build number derived
from `VERSION`. Release runs those same analysis and archive steps and retains
the unsigned archive for Publish to sign and export. Simulator coverage does
not claim physical-device VPN traffic.

Publish signs and exports the selected iOS archive with the configured Apple
certificates and provisioning profiles. Run Swift lifecycle tests on macOS
with:

    python3 .github/scripts/source_checks.py swift-unit

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
source. It compares the resulting unsigned APK with the Release app's payload;
the check uses a disposable test-signed reference APK, not the production
signing key.

The Android recipe pins the Go toolchain and Compose dependencies used by the
product build. Historical changelogs remain as published release records.

## iOS signing and release metadata

The iOS signing check validates app and extension signatures, provisioning
profiles, bundle identifiers, App Group, source revision, version, build
number, and packet-tunnel entitlement before uploading the package as a
run-scoped artifact.

Release provenance records asset checksums and source metadata. It contains no
credentials or private test evidence.
