# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- iOS 26 diagnostic logging: detailed interface tracking (BEFORE/AFTER tunnel)
- iOS 26 research logging: iOS version, device info at startup
- Go tunnel: outbound connection tracking showing local address (diagnoses tunnel bypass)
- CRITICAL warning when outbound connections NOT using VPN tunnel (local != 198.18.x.x)
- Enhanced PATH_UPDATE with timestamps and NETWORK_CHANGED events
- iOS socket protection research: IP_BOUND_IF option attempt

### Fixed
- Improved error messages for StreamDialer and PacketDialer failures

### Documentation
- Added iOS 26 socket options research notes

---

## [1.x.x] - YYYY-MM-DD

### Changed
- Initial AGENTS.md documentation

[1.x.x]: https://github.com/DobbyVPN/DobbyVPN/releases/tag/v1.x.x
