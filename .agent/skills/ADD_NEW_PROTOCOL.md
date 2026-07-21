---
name: add-new-protocol
description: Step-by-step instructions on how to add a new VPN protocol (engine) into DobbyVPN across all platforms (Go backend, Android/Desktop KMP frontend, iOS Swift).
---

# Add New Protocol

This guide outlines the architecture and exact steps to add a new VPN protocol to DobbyVPN. 

## App Architecture Review

### go_module (Core Protocol Logic)
The core VPN engine logic resides in `go_module`. There are two integration patterns:
1. **ProtocolDevice Pattern (Standard)**: For protocols that provide a local SOCKS5 proxy (like Outline, Xray). You implement `core/pkg.ProtocolDevice` and return the proxy address. The `core` package handles `tun2socks` automatically.
2. **Standalone Pattern**: For protocols that manage the TUN interface themselves (like AmneziaWG).

Exports for different platforms:
- **Desktop (Windows, macOS, Linux)**: Uses gRPC. Handlers are in `desktop_exports/proto/` which call `desktop_exports/api/protocols_core.go`.
- **Android**: Uses `gomobile`. Exports are in `kotlin_exports/dobby_vpn.go`.
- **iOS**: Uses `gomobile`. Exports are in `ios_exports/dobby_vpn.go`.

### kmp_module (Android & Desktop Frontend)
Written in Kotlin Multiplatform (KMP).
- **Domain**: Config parsing (`TomlConfigApplier`), repositories (`DobbyConfigsRepository`), and the `VpnInterface` enum.
- **Facades**: Protocol integration uses `LibFacade` interfaces in `com.dobby.feature.vpn_service`.

### swift_module (iOS Frontend & Tunnel)
- The Network Extension (`tunnel/` target) handles VPN lifecycle.
- Protocol dispatch is in `PacketTunnelProvider.swift`.
- Each protocol has an Interactor (e.g., `XRayInteractor.swift`) that calls the gomobile-generated `Cloak_outline` framework functions.
- Configs are read from shared `UserDefaults` via `DobbyConfigsRepositoryImpl.swift`.

---

## How to Add a New Protocol (ProtocolDevice Pattern)

Follow these steps precisely:

### Step 1: Implement the Go Backend (`go_module`)
1. Create a new package (e.g., `go_module/newproto/newproto_device.go`).
2. Implement the `pkg.ProtocolDevice` interface:
   - `Open(routingTableID int, uplinkIface string) error` (starts engine)
   - `GetProxyAddr() string` (returns local SOCKS5 bridge address)
   - `GetServerIP() net.IP` (returns remote VPN server IP for routing)
   - `Close() error` (stops engine)
3. **Desktop Exports**: 
   - Update `grpcproto/vpnserver.proto` with new RPCs (e.g., `StartNewProto`, `StopNewProto`). Run protoc.
   - Create `desktop_exports/proto/newproto.go` to implement the gRPC handlers.
   - Update `desktop_exports/api/protocols_core.go` inside the `startVpn()` switch statement.
4. **Android Exports**: Update `kotlin_exports/dobby_vpn.go` inside the `NewVpnClient()` switch statement.
5. **iOS Exports**: Update `ios_exports/dobby_vpn.go` inside the `NewVpnClient()` switch statement.

### Step 2: Implement the Kotlin Multiplatform Frontend (`kmp_module`)
1. Update `VpnInterface` enum in `com.dobby.feature.main.domain.DobbyConfigsRepository.kt`.
2. Create config data classes and a `NewProtoTomlApplier.kt` in `com.dobby.feature.main.domain.config`.
3. Register the new applier in `TomlConfigApplier.kt`.
4. Create a repository interface `DobbyConfigsRepositoryNewProto` and implement it where needed.
5. Create a facade (e.g., `NewProtoLibFacade.kt`) in `com.dobby.feature.vpn_service` for Android/Desktop platform-specific wiring.

### Step 3: Implement the iOS Frontend (`swift_module`)
1. Update `swift_module/CommonDI/DobbyConfigsRepositoryImpl.swift` to map the new protocol string to the `VpnInterface` enum.
2. Create `swift_module/tunnel/NewProtoInteractor.swift` to handle starting/stopping the protocol via `Cloak_outline...` gomobile calls.
3. Update `swift_module/tunnel/PacketTunnelProvider.swift`:
   - Add the new interactor as a class property.
   - In `startTunnel()`, add an `else if vpnInterface == VpnInterface.newProto` branch to dispatch to your interactor.
   - In `teardownForStop()`, ensure your interactor's stop method is called.
