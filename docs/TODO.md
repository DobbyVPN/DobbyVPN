# Deferred qualification work

These items are not part of the current passing qualification suites and are
not accepted unavailable skips:

- Add Android TrustTunnel tunnel-traffic qualification on an arm64 device and
  x86_64 emulator.
- Add live-server TrustTunnel qualification with a known-good test profile and
  verify connect, traffic, disconnect, and cleanup on each supported platform.
- Add physical-device iOS qualification for NetworkExtension permission,
  tunnel traffic, routing, disconnect, and cleanup.
- Define suspend/resume recovery checks where the platform lifecycle can be
  controlled reliably.
- Reconsider the diagnostic-only network-transition scenario after a runner
  can test it without losing its control path.

A Linux GUI is outside the current scope. Linux remains CLI/service-only.
The supported mini and full suite coverage is owned by the
[functional contract](../torturer/docs/contract.md).
