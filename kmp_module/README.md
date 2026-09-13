# Shared Compose client

A Kotlin Multiplatform app that keeps one shared Compose UI across Android,
iOS, Linux, Windows, and macOS. Go owns configuration parsing, protocol
selection, session lifecycle, and runtime resources in
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
├── app/ --- shared Compose UI and thin platform shells
├── grpcprotos/ --- canonical session/Diagnostics schema
├── grpcstub/ --- typed desktop gRPC calls
└── iosApp/
```

Do not add a protocol-specific UI toggle, KMP repository, another session
manager in Kotlin or Swift, or protocol-specific start/stop RPCs. New protocols
enter through the Go `ProtocolDevice` session extension path described in
[`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md).
