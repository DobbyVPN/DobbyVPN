# Deferred work

## Coverage

These checks are not part of the current passing qualification suites and are
not accepted unavailable skips:

- Add Android TrustTunnel tunnel-traffic qualification on an arm64 device and
  x86_64 emulator; extend physical arm64 device tunnel-traffic qualification to
  Outline and Xray.
- Add live-server TrustTunnel qualification with a known-good test profile and
  verify connect, traffic, disconnect, and cleanup on each supported platform.
- Add physical-device iOS qualification for NetworkExtension permission,
  tunnel traffic, routing, disconnect, and cleanup.
- Add targeted Go fuzz tests for untrusted configuration and control-message
  parsers. Keep seed cases in the existing unit suite and consider bounded
  periodic fuzzing after the targets prove useful.

## Execution time

- Reduce measured qualification time in the Mac/iOS local sequence, the CI iOS
  Simulator job, and Android/F-Droid builds while preserving coverage and pass
  criteria.

A Linux GUI is outside the current scope. Linux remains CLI/service-only.
The supported mini and full suite coverage is owned by the
[functional contract](../torturer/docs/contract.md).
