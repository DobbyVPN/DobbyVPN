# Running the native Apple apps

The macOS and iOS frontends are SwiftUI. They use the shared Go backend for
configuration, profile selection, VPN state, protocols, recovery, and
diagnostics.

## iOS Simulator

Open swift_module/iosApp.xcodeproj in Xcode, select the iosSimulatorApp scheme,
and run it on an iOS Simulator. The Test workflow builds the Go runtime
XCFramework, packages the Simulator app, and runs the XCTest UI contract.

The Simulator check covers visible SwiftUI controls and app lifecycle. It does
not establish a physical NetworkExtension tunnel or validate physical-device
VPN traffic. The Simulator package uses ad-hoc signing and does not need an
Apple development certificate.

To build only the Go Simulator runtime slice on a Mac, install the pinned
gomobile and gobind tools recorded by go_module/go.mod, then run:

    cd go_module
    ./scripts/build_ios_xcframework.sh --simulator-architecture arm64

Use amd64 on an Intel Mac.

## macOS

The macOS SwiftUI app is built on macOS with:

    python3 .github/scripts/desktop_build.py native-ui --platform macos --output <app-path>

For local UI work, the Go backend also needs to be installed or running as the
user's local launchd service. The app talks to it through the Unix control
socket. Release packages install the app and Go backend together.

## Swift lifecycle tests

Run the platform-neutral Swift lifecycle tests with:

    swift test --enable-code-coverage --package-path swift_module

These tests cover native provider command and response behavior. The iOS
Simulator XCTest target separately checks the rendered SwiftUI app.
Physical-device builds require the Apple signing identities and provisioning
profiles configured for Release.
