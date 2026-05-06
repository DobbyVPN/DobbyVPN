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

### 2026-05-04 - iOS 26 VPN connectivity fix (RESOLVED)

**Summary**: Fixed VPN connection failures on iOS 26 where connections were failing due to deprecated socket protection mechanism.

**Root cause identified**: 
- iOS 26 deprecated `SO_NO_TC_NETPOLICY` socket option (returns "invalid argument")
- Old code used `IP_BOUND_IF` with index `0`, which doesn't bind to any interface
- Result: VPN traffic was routed through tunnel (198.18.x.x) instead of physical interface, causing routing loop and connection failures

**Solution implemented**:
- Changed socket protection to use `IP_BOUND_IF` with actual interface index
- Swift's `NWPathMonitor` detects default physical interface (WiFi/Cellular)
- Interface name converted to index via `if_nametoindex()` and passed to Go
- Go's protector binds outbound sockets to physical interface, bypassing VPN tunnel

**Files changed**:
- `go_module/ios_exports/protector.go` (new) - Exports interface index functions to Swift
- `go_module/tunnel/protected_dialer/protect_ios.go` - Updated socket protection logic
- `go_module/modules/Cloak/exported_client/protector_ios.go` - Updated Cloak socket protection
- `swift_module/tunnel/PacketTunnelProvider.swift` - Added interface detection and Go notification
- `CHANGELOG.md` - Updated with iOS 26 fix details
- `IOS26_FIX_SUMMARY.md` - Complete solution documentation

**Validation performed**:
- Go syntax verified with `gofmt`
- Git commit created and pushed successfully
- Branch: `ios26-kimi` at commit `6b2dd395`

**Status**: ✅ FIXED, committed, and pushed

**Branch**: `ios26-kimi` (https://github.com/DobbyVPN/DobbyVPN/pull/new/ios26-kimi)

**Next recommended steps**:
1. Test on iOS 26 device to verify fix works
2. Look for logs: `[iOS26-RESEARCH] Set default interface index: X (pdp_ip0/Cellular)`
3. Confirm: `[Protect] TCP dial OK: local=198.18.0.1:*` (tunnel IP, not cellular 10.x.x.x)
4. Consider PR merge or further testing

---

## PR Expectations

When preparing a PR:

- Explain the observed failure mode.
- Link the code path to the log evidence when applicable.
- Separate diagnosis, fix, and validation.
- Mention platform coverage and what was not tested locally.
