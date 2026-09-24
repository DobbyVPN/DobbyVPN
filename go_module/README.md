# Go backend and CLI

This module owns configuration acquisition and parsing, accepted subscription
URL persistence, session policy and generation state, profile selection,
protocol construction, routing and TUN resources, probes, recovery, cleanup,
and local diagnostics. Outline, Xray, and TrustTunnel remain supported.

The native frontends use the shared session API. Android and iOS call it through
the mobile binding. The macOS and Windows frontends use the local JSON control
endpoint provided by the Go backend: a Unix domain socket on macOS and a named
pipe on Windows. The Go CLI uses the same backend directly. There is no desktop
gRPC control server.

## Go tests

On Linux, first stage the pinned native TrustTunnel bridge and C++ runtime:

    python3 .github/scripts/desktop_build.py prepare-go-test-deps --go-mod-tidy

Then run from this directory:

    go test -tags=ci ./...
    go test -race ./routing/... ./sessionapi/... ./tunnel/...

The setup command changes only its own environment. The Test workflow runs the
same Go test targets after preparing those dependencies.

## Desktop backend and CLI

The desktop build helper builds the Go backend and operator CLI for the current
host. Use the native target host when building Windows or macOS binaries:

    python3 .github/scripts/desktop_build.py libs --with-cli

The backend runs as the installed Windows Service, launchd daemon on macOS, or
systemd service on Linux. On Unix systems, the frontend and CLI connect through
a local socket. Windows uses the fixed DobbyVPN.Control named pipe. The Windows
pipe ACL restricts access to the installed service user and rejects remote
clients.

The CLI supports connect, connect-profile, check-config, profile-inventory,
disconnect, status, logs clear, external-ip, and verify-session. It is intended
for operator commands and scripts; the native frontends send requests directly
to the backend for each action.

## Android runtime

Android uses a Kotlin/Compose frontend and a Go backend library. Build the
release app from android_module with the pinned Go compiler:

    cd android_module
    ./gradlew -PdobbyGoBinary="$(go env GOROOT)/bin/go" :app:assembleRelease

The Go runtime is packaged for arm64-v8a and x86_64. TrustTunnel is available
only on arm64-v8a; x86_64 returns the typed unsupported-protocol result.

## iOS runtime

The Go NetworkExtension runtime is built as an XCFramework. The visible iOS UI
is SwiftUI in swift_module, and the tunnel provider uses the Go mobile binding.
The pinned gomobile and gobind tools are recorded in go.mod.

For a Simulator architecture, use the package build script on macOS with the
pinned Go toolchain and mobile tools installed:

    ./scripts/build_ios_xcframework.sh --simulator-architecture arm64

The default script builds both a physical-device slice and a universal
Simulator slice for Release. Simulator packaging does not require an Apple
development certificate. Physical-device packaging uses the signing identities
and profiles provided by Release.

## Desktop JSON control

The local desktop endpoint exposes Snapshot, Configure, Start, and Stop.
Frontends poll Snapshot for state and use session ID, sequence, and generation
values to reject stale commands and responses. After a backend restart, a
`NOT_FOUND` Snapshot response means the frontend must reattach with a new
Snapshot request without a session ID. The Go backend owns the persisted
accepted subscription URL; an inline TOML source stays in the frontend's
current session.

The JSON transport is local to the machine. Unix control sockets are protected
by filesystem ownership and permissions. The Windows named pipe has a fixed
name and a local access list. Do not add a second protocol or command wrapper
without a concrete requirement.
