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
