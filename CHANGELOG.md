# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **iOS 26 VPN connectivity**: Fixed socket protection mechanism for iOS 26+ where `SO_NO_TC_NETPOLICY` socket option was deprecated
  - Changed socket protection from `SO_NO_TC_NETPOLICY` to `IP_BOUND_IF` with actual interface index
  - Swift side now detects default network interface (WiFi/Cellular) and passes interface index to Go via `if_nametoindex()`
  - Added support for both IPv4 (`IP_BOUND_IF`) and IPv6 (`IPV6_BOUND_IF`) socket binding
  - Outbound VPN connections now correctly bypass tunnel and use physical interface

### Added
- New Go module file: `go_module/ios_exports/protector.go` - exports `SetDefaultInterfaceIndex()` to Swift
- Interface detection in `PacketTunnelProvider.swift` using `NWPathMonitor` and `if_nametoindex()`
- iOS 26 diagnostic logging: detailed interface tracking (BEFORE/AFTER tunnel)
- iOS 26 research logging: iOS version, device info at startup
- Go tunnel: outbound connection tracking showing local address (diagnoses tunnel bypass)
- Enhanced PATH_UPDATE with timestamps and NETWORK_CHANGED events

### Documentation
- Added `IOS26_FIX_SUMMARY.md` with complete solution documentation

---

## [1.x.x] - YYYY-MM-DD

### Changed
- Initial AGENTS.md documentation

[1.x.x]: https://github.com/DobbyVPN/DobbyVPN/releases/tag/v1.x.x
