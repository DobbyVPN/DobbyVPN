# Testing DobbyVPN

The public repository keeps tests that contributors can run without private
infrastructure or credentials.

## Local source checks

From `go_module/`:

```bash
go test ./...
go test -race ./core/... ./routing/... ./sessionapi/... ./tunnel/...
```

From `kmp_module/` with JDK 17 and the Android SDK configured:

```bash
./gradlew test detekt :app:testDebugUnitTest
```

On an Apple-silicon Mac with Xcode 26.3 and an installed iOS runtime:

```bash
swift test --enable-code-coverage --package-path swift_module
cd kmp_module
./gradlew :app:linkDebugFrameworkIosSimulatorArm64 :app:iosSimulatorArm64Test
```

To build and install the complete unsigned Simulator app locally, first build
the public Go framework from `go_module/`, then stage the matching KMP
framework before invoking Xcode:

```bash
cd go_module
./scripts/build_ios_xcframework.sh
ditto MyLibrary.xcframework ../swift_module/MyLibrary.xcframework

cd ../kmp_module
./gradlew :app:linkDebugFrameworkIosSimulatorArm64 :app:iosSimulatorArm64Test
rm -rf ../swift_module/app.framework
ditto app/build/bin/iosSimulatorArm64/debugFramework/app.framework ../swift_module/app.framework

SIMULATOR_UDID="$(xcrun simctl list devices available -j | jq -r '[.devices[][] | select(.isAvailable and (.name | startswith("iPhone")))][0].udid')"
xcrun simctl boot "$SIMULATOR_UDID" || true
xcrun simctl bootstatus "$SIMULATOR_UDID" -b
xcodebuild build -project ../swift_module/iosApp.xcodeproj -scheme iosApp \
  -configuration Debug -sdk iphonesimulator \
  -destination "platform=iOS Simulator,id=$SIMULATOR_UDID" \
  -derivedDataPath /tmp/dobbyvpn-ios-simulator \
  CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY=""
xcrun simctl install "$SIMULATOR_UDID" \
  /tmp/dobbyvpn-ios-simulator/Build/Products/Debug-iphonesimulator/doBBYVPN.app
```

For an Intel Mac, replace `iosSimulatorArm64` with `iosX64` in the Gradle task
and framework path. The hosted public workflow runs the Apple-silicon variant.

The Swift package compiles the exact platform-neutral production source from
`swift_module/CommonDI`; it is not a copied lifecycle model. The Gradle command
links the KMP Simulator framework and executes `commonTest` coverage inside an
iOS Simulator. Its deterministic tests include the extension-process Go
session transaction (create/configure/start/poll/stop/destroy), including
virtual-time timeout and cleanup retry paths. `iosX64` is also declared for
Intel macOS environments.

Simulator checks cover shared parsing, mapping, lifecycle generation fences,
observation sequencing, retry decisions, framework linkage, and any future
synthetic app tests. They cannot prove Packet Tunnel Provider execution,
NetworkExtension routing/DNS, real traffic, entitlements, signing, sleep/wake,
or device resource cleanup. Those assertions require a physical iPhone.

The Go XCFramework intentionally includes a Simulator slice. It shares all
session/runtime code with the device slice, but TrustTunnel returns a typed
unsupported error because its vendor-supplied native bridge is physical-iOS
only. This keeps the Simulator app loadable without pretending to validate a
VPN protocol it cannot execute.

## Independent public verification

Pull requests also call
[`DobbyVPN/Torturer`](https://github.com/DobbyVPN/Torturer) at an immutable
commit. Torturer source-builds the exact pull-request revision on hosted Linux,
Windows, macOS ARM, macOS Intel, and Android runners, then exercises only
secretless product-facing contracts and synthetic invalid input.

Torturer's iOS Simulator helper is intentionally staged: its public evidence
contract lands first, then a later immutable Torturer revision may add a
secretless hosted Simulator job after this repository exposes a stable
Simulator app/XCTest target. The workflow pin is never advanced in the same
unreviewed change as the helper it executes.

The caller uses the unprivileged `pull_request` event, read-only permissions,
no secrets, no protected environments, and no shared Actions cache.

## Scope boundary

Public tests intentionally use no provider credentials or real endpoint
configuration. They do not claim real external-network identity, sustained
throughput, or physical-device NetworkExtension qualification.
