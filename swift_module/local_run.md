# Guide to running the iOS app locally

The release app's visible controls are rendered by Go/Fyne. Swift remains the
containing-app and NetworkExtension shell: it owns app-group storage,
permission/provider setup, the one-shot Keychain mailbox, and the C bridge
called by the Go process.

## Simulator

On a Mac with Xcode and the pinned Go toolchain:

```bash
cd go_module
go mod download
go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260520154334-0e4426e1883d
go install golang.org/x/mobile/cmd/gobind@v0.0.0-20260520154334-0e4426e1883d
./scripts/build_ios_xcframework.sh --simulator-architecture arm64
./scripts/package_ios_app.sh iossimulator /tmp/Dobby-Vpn.app \
  DobbyVPNRuntime.xcframework arm64
xcrun simctl install booted /tmp/Dobby-Vpn.app
xcrun simctl launch booted vpn.dobby.app
```

Use `amd64` on an Intel Mac. Simulator packaging supplies temporary
self-signed metadata to the pinned Fyne packager and ad-hoc signs the bundle;
an Apple Development certificate or provisioning profile is not required.
The Simulator XCTest UI target validates the rendered Go/Fyne controls through
real accessibility lookup, taps, keyboard typing, and app terminate/reopen
lifecycle. It cannot validate a physical NetworkExtension tunnel or
TrustTunnel. App logs are diagnostic output, not the UI pass condition.

## Physical-device/App Store build

The Release workflow downloads `DobbyVPNRuntime.xcframework`, installs the
Apple distribution certificate and both provisioning profiles, builds the
CommonDI/tunnel frameworks, and invokes:

```bash
./go_module/scripts/package_ios_app.sh ios swift_module/build/ipa/DobbyVPN.ipa \
  swift_module/DobbyVPNRuntime.xcframework
```

Set `IOS_CERTIFICATE_NAME`, `IOS_PROFILE_NAME`, and `IOS_SIGNING_IDENTITY` to
the identities installed by the signing job. This device path intentionally
requires the Apple distribution credentials; that requirement does not apply
to the Simulator path above.

## Swift lifecycle tests

```bash
swift test --enable-code-coverage --package-path swift_module
```

These tests cover the native provider command/response and cleanup policy.
They do not replace the Go UI tests or a real device VPN run.
