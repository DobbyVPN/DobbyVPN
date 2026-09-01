# Testing DobbyVPN

The public repository keeps tests that contributors can run without private
infrastructure or credentials.

## Local source checks

From `go_module/`:

```bash
go test ./...
go test -race ./routing/... ./sessionapi/... ./tunnel/...
```

From `kmp_module/` with JDK 17 and the Android SDK configured:

```bash
./gradlew :grpcstub:test :app:jvmTest :app:testDebugUnitTest :app:verifyDebugNativeAbiPayloads
./gradlew :app:detektMetadataCommonMain :app:detektJvmMain :grpcstub:detekt
```

Use the source-set-specific Detekt tasks above. The root KMP aggregate
`detekt` task has no sources and is not lint evidence.

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
ditto DobbyVPNRuntime.xcframework ../swift_module/DobbyVPNRuntime.xcframework

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
python3 .github/scripts/run_ios_simulator_app_lifecycle.py \
  --device "$SIMULATOR_UDID" \
  --app /tmp/dobbyvpn-ios-simulator/Build/Products/Debug-iphonesimulator/doBBYVPN.app \
  --screenshots /tmp/dobbyvpn-ios-simulator-screenshots \
  --result /tmp/dobbyvpn-ios-simulator-lifecycle.json
```

`--screenshots` is deliberately an owner-local option. Public GitHub workflows
run the same lifecycle assertions without retaining or uploading screenshots.

For an Intel Mac, replace `iosSimulatorArm64` with `iosX64` in the Gradle task
and framework path. The hosted public workflow runs the Apple-silicon variant.

The Swift package compiles the exact platform-neutral production source from
`swift_module/CommonDI`; it is not a copied lifecycle model. The Gradle command
links the KMP Simulator framework and executes `commonTest` coverage inside an
iOS Simulator. Its deterministic tests include the extension-process Go
session transaction (create/configure/start/observe/stop/destroy), including
virtual-time timeout and cleanup retry paths. It also executes the shared
logging contract for legacy-record compatibility, full-timestamp ordering,
multi-producer merge/clear, and durable retention of the latest clear marker.
`iosX64` is also declared for Intel macOS environments.

Simulator checks cover shared parsing, mapping, lifecycle generation fences,
observation sequencing, retry decisions, framework linkage, fresh install,
retained-data reinstall, cold and repeated launch, background/foreground,
forced termination/relaunch, and (owner-locally) real foreground app
screenshots. The lifecycle helper verifies that every launched process remains
alive after the startup window, not merely that launchd accepted a request.
Shared storage tests cover missing, corrupt, unwritable, and full diagnostic
storage and require controlled degradation rather than startup failure.

The signed-IPA workflow separately inspects the app and packet-tunnel extension,
signatures, exact entitlements, App Group, source commit, version/build,
provisioning expiry, and release debugger policy.

The Go XCFramework intentionally includes a Simulator slice. It shares all
session/runtime code with the device slice, but TrustTunnel returns a typed
unsupported error because its vendor-supplied native bridge is physical-iOS
only. This keeps the Simulator app loadable without pretending to validate a
VPN protocol it cannot execute.

## Owner-controlled Android transition seam

The instrumentation-only hosted-profile driver also accepts the canonical
`network_transition`, `sleep_wake`, and `process_loss` operations. These are
not production controls and do not add a Harness or Torturer dependency to the
application: an owner-side adapter performs the emulator action, then signals
the test APK through a one-use, token-bound private-file rendezvous. The app
reports only the resulting tunnel, routing, or disconnection facts; the adapter
retains the complete control command diagnostics and proves the emulator state
change. Missing or malformed control input fails closed, and ordinary commands
cannot include the control fields.

## Independent public verification

Pull requests also call
[`DobbyVPN/Torturer`](https://github.com/DobbyVPN/Torturer) at an immutable
commit. Torturer source-builds the exact pull-request revision on hosted Linux,
Windows, macOS ARM, macOS Intel, and Android runners, then exercises only
secretless product-facing contracts and synthetic invalid input. Its iOS
Simulator lane builds the Go Simulator framework, runs the production Swift
suite and KMP `iosSimulatorArm64Test`, then builds, installs, launches,
and terminates the unsigned app. Shared `commonTest` additions therefore
extend both DobbyVPN's own Simulator job and the independent Torturer lane
without duplicating tests. A named app XCTest remains a separate future stage.

The caller uses the unprivileged `pull_request` event, read-only permissions,
no secrets, no protected environments, and no shared Actions cache.
It invokes Torturer's secretless verification workflow only; it cannot create
provider resources or access the trusted functional environment.

After a successful exact-commit Release and internal TestFlight upload,
Torturer owns the trusted hosted functional lanes for Linux, Windows, macOS,
and Android. One narrow retrieval job validates that Release and stages only
its exact packages; platform jobs install them without checking out or
rebuilding DobbyVPN, then run Torturer's canonical scenario engine against one
disposable Render-hosted Outline WebSocket server per platform. The provider
credential and plaintext profile remain confined to Torturer's protected
server-lease job. Candidate jobs receive neither provider nor cross-repository
credentials, and DobbyVPN does not import or depend on Torturer.

Hosted results contain the Torturer assertions, measurements, and cleanup
state. Complete raw VPN application and service logs are uploaded only after
Torturer has deleted every disposable Render service for the run and confirmed
that they are absent. Raw profiles are never uploaded. Private-profile coverage
and complete local VPN logs remain owner-local.

## Scope boundary

Pull-request tests intentionally use no provider credentials or real endpoint
configuration. Trusted hosted functional tests use only a disposable profile
and server. Public raw-log upload is blocked unless disposal is confirmed.
Private-profile coverage and complete local diagnostics remain outside this
public repository.
