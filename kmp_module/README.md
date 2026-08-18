# Shared Compose client

A Kotlin Multiplatform client that keeps one shared Compose presentation layer
across Android, iOS, Linux, Windows, and macOS. Go owns configuration parsing,
protocol selection, SessionV2 lifecycle, and runtime resources in
[`go_module/`](../go_module/); platform shells only provide the OS VPN
permission, TUN, socket-protection, and extension/service callbacks.

## Prerequisites

- Java 17+
- Golang
- Android SDK with NDK support

## Build

```bash
./gradlew assembleDebug
```

## Architecture

The module is intentionally a shared UI and binding layer:

```
kmp_module/
├── app/ --- shared Compose UI, SessionV2 presentation, and thin shells
├── grpcprotos/ --- canonical SessionV2/Diagnostics schema
├── grpcstub/ --- protocol-neutral SessionV2 transport mapping
├── iosApp/
└── outline/ --- retained vendor integration support
```

Do not add a protocol-specific UI toggle, KMP repository, Swift lifecycle
owner, or separate start/stop RPC. New protocols enter through the Go
`ProtocolDevice`/SessionV2 extension path described in
[`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md).
