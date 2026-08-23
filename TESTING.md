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

Torturer separately owns manually dispatched, trusted hosted functional lanes
for Linux, Windows, macOS, and Android. Those lanes source-build an exact
DobbyVPN commit, stage a strict runtime allow-list, and run Torturer's canonical
scenario engine against one disposable Render-hosted Outline WebSocket server
per platform. The provider credential and plaintext profile remain confined to
Torturer's protected server-lease job. Candidate build jobs never receive
provider credentials, and DobbyVPN does not import or depend on Torturer.

Hosted results contain only the canonical assertions, bounded safe metrics,
exact source/runtime provenance, and verified cleanup state. They do not upload
screenshots, raw profiles, credentials, endpoint URLs, or literal external-IP
observations. Owner-local qualification remains responsible for complete raw
evidence, screenshots, private profile coverage, and OS-specific diagnostics.

## Scope boundary

Pull-request tests intentionally use no provider credentials or real endpoint
configuration. Trusted hosted functional lanes use only a run-scoped disposable
profile and server, and publish no credential-bearing evidence. Private profile
coverage and complete local diagnostics remain outside this public repository.
