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
./gradlew :app:linkDebugFrameworkIosSimulatorArm64 :app:iosSimulatorArm64Test --rerun-tasks --no-daemon
```

On an Intel Mac with Xcode 26.3 and an installed iOS runtime:

```bash
cd kmp_module
./gradlew :app:linkDebugFrameworkIosX64 :app:iosX64Test --rerun-tasks --no-daemon
```

The public iOS Simulator app lifecycle is owned by the pinned Torturer
workflow. It prepares the Go and KMP frameworks through the product build
interfaces, then invokes Torturer's canonical app-contract command. The same
command supports `--architecture amd64 --startup-only` on the Intel local Mac.
The standalone KMP checks above remain the local CPU-rendered native checks.

For initialization-only testing, adding `DOBBY_STARTUP_TEST` to Xcode's Swift
active compilation conditions builds a Simulator-only variant. It executes
the normal session-bridge and dependency initialization, records
`startup.initialized mode=startup-only` in the canonical app log, and omits
the Compose window. It is not normal app UI launch, Metal, input, or VPN-traffic
coverage. Normal builds do not set this flag and keep the Compose UI; the
condition cannot omit the UI on physical-device builds. This test mode exists
to isolate initialization from graphics and can be removed if that separate
check is retired. It does not require a modified Compose dependency.

The Swift package compiles the exact platform-neutral production source from
`swift_module/CommonDI`; it is not a copied lifecycle model. The Gradle command
links the KMP Simulator framework and executes `commonTest` coverage inside an
iOS Simulator. Its deterministic tests include the extension-process Go
session transaction (create/configure/start/observe/stop/destroy), including
virtual-time timeout and cleanup retry paths. It also executes the shared
logging contract for legacy-record compatibility, full-timestamp ordering,
multi-producer merge/clear, and durable retention of the latest clear marker.
`iosX64` is also declared for Intel macOS environments.

Simulator checks cover shared parsing, mapping, lifecycle generation handling,
observation sequencing, retry decisions, framework linkage, fresh install and
one canonical launch/termination lifecycle. The app-contract helper verifies
that the launched process remains alive after the startup window, not merely
that launchd accepted a request.
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

The instrumentation-only hosted-profile driver accepts the canonical
`network_transition` operation. This is not a production control and does not
add a Harness or Torturer dependency to the application:
an owner-side adapter performs the emulator action, then signals the test APK
through a run-scoped file rendezvous. Torturer proves process
loss externally by force-stopping the production app, observing its absence,
and starting a fresh companion session.

Suspend/resume is a known untested limitation on every platform. The functional
contract contains no sleep/wake operation until a controlled environment can
perform and observe real system suspend and resume.

## Independent public verification

There is no pull-request source-build contract. After merge, GitHub Release
builds the publishable packages. The product Test workflow runs as part of
that Release, including the iOS Simulator shared-core checks. A named app
XCTest remains a separate future stage.

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

Pull-request product jobs are not the functional test set. Trusted hosted
functional tests use only a disposable profile and server. Public raw-log
upload is blocked unless disposal is confirmed. Private-profile coverage and
complete local diagnostics remain outside this public repository.
