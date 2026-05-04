# AGENTS.md

## Project Shape

DobbyVPN is a multiplatform VPN client.

- `go_module/`: shared native VPN/protocol engine, gomobile exports, desktop gRPC server.
- `kmp_module/`: Kotlin Multiplatform app logic, Android app/service code, shared state/config/health checks.
- `swift_module/`: iOS/macOS Swift app, NetworkExtension tunnel provider, Swift-to-Go bridge.
- `installer/`: desktop packaging.
- `tester/`: helper tooling.

## Important VPN Architecture Notes

- Android gets an explicit TUN fd from `VpnService.Builder.establish()` and passes it to Go.
- iOS uses `NEPacketTunnelProvider`; be careful around packet tunnel startup order, network settings, and Go bridge assumptions.
- Cloak profiles rewrite Outline to local `127.0.0.1`; Cloak must be started before Outline traffic depends on that listener.
- Startup errors from native Go code must propagate to platform code. Do not report VPN connected if Cloak/Outline startup failed.
- Teardown must explicitly stop native engines before clearing tunnel state.

## Validation

Prefer targeted checks before broad builds.

- Go formatting: `gofmt -w <changed-go-files>`
- Go tests from `go_module/`: `go test ./...` or affected packages
- Kotlin checks from `kmp_module/`: `./gradlew detekt`
- Android build from `kmp_module/`: `./gradlew assembleDebug`
- Swift lint from `swift_module/` on macOS: `swiftlint lint --config .swiftlint.yml`
- Always run: `git diff --check`

If gomobile/iOS artifacts cannot be rebuilt in the current environment, state that explicitly in the final notes.

## Code Change Rules

- Do not commit log files, downloaded configs, secrets, VPN keys, or local investigation notes.
- Treat TOML subscription/config files as potentially sensitive.
- Avoid broad lifecycle refactors unless backed by logs or a reproducible race.
- Preserve platform differences instead of forcing Android/iOS/Desktop into one abstraction when VPN APIs differ.
- Do not modify generated bindings unless the generator/source change is also included or the reason is documented.

## Known High-Value Follow-Ups

- Replace iOS utun fd scanning with a supported `NEPacketTunnelProvider.packetFlow` based design or another explicit supported bridge.
- Make iOS upstream socket bypass/protection deterministic and observable.
- Align iOS `NEPacketTunnelNetworkSettings.mtu` with the Go tun2socks engine MTU instead of hardcoding divergent values.

## Session handoff notes

### 2026-05-03/04 - iOS 26 VPN connectivity diagnosis

**Summary**: Investigating VPN connection failures on iOS 26 where:
- Cellular: completely fails
- WiFi: starts but becomes unstable with chacha20poly1305 auth failures

**Root cause investigation**: The issue is NOT about cellular/expensive/constrained flags. The problem occurs even with cellular disabled. The "other" interface (utun5) appears at VPN startup regardless of cellular.

**Files touched**:
- `swift_module/tunnel/PacketTunnelProvider.swift` - Added iOS 26 research logging
- `go_module/tunnel/protected_dialer/protect_ios.go` - Added IP_BOUND_IF research
- `go_module/tunnel/protected_dialer/dialer.go` - Added outbound connection local address logging
- `go_module/outline/internal/outline_device.go` - Enhanced error messages

**Key diagnostic added**: 
- `[Protect] *** CRITICAL *** Outbound TCP connection NOT using VPN tunnel! local=192.168.x.x`
- This will show if iOS 26 is bypassing the tunnel for outbound connections

**Validation**: Code compiles, branch pushed successfully

**Next steps**:
1. Rebuild iOS app with new logging
2. Test on iOS 26 with WiFi (no cellular)
3. Look for CRITICAL message in logs - if local=192.168.x.x, that confirms tunnel bypass
4. If bypass confirmed, need to find iOS 26 socket protection alternative to SO_NO_TC_NETPOLICY

**Remaining risks**:
- Root cause not yet confirmed - pending new test logs
- May need to research iOS 26-specific socket options or Network.framework

---

## PR Expectations

When preparing a PR:

- Explain the observed failure mode.
- Link the code path to the log evidence when applicable.
- Separate diagnosis, fix, and validation.
- Mention platform coverage and what was not tested locally.
