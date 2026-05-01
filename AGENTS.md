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

## PR Expectations

When preparing a PR:

- Explain the observed failure mode.
- Link the code path to the log evidence when applicable.
- Separate diagnosis, fix, and validation.
- Mention platform coverage and what was not tested locally.
