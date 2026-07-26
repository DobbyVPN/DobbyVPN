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

Platform-specific Swift and Android instrumentation targets run when their
toolchains or devices are available. See the platform directories and CI
workflows for the exact maintained commands.

## Independent public verification

Pull requests also call
[`DobbyVPN/Torturer`](https://github.com/DobbyVPN/Torturer) at an immutable
commit. Torturer source-builds the exact pull-request revision on hosted Linux,
Windows, macOS ARM, macOS Intel, and Android runners, then exercises only
secretless product-facing contracts and synthetic invalid input.

The caller uses the unprivileged `pull_request` event, read-only permissions,
no secrets, no protected environments, and no shared Actions cache.

## Maintainer release qualification

Real-provider profiles, external network identity, sustained performance,
cleanup, controlled VMs/devices, and physical iOS NetworkExtension checks use
maintainer-controlled infrastructure. They are not needed to build or audit
DobbyVPN and never run in a contributor pull-request context.
