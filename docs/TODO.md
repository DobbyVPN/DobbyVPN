# Deferred product qualification

These checks are intentionally outside the 1.5.1 Go/Fyne migration acceptance
set. They remain product test work rather than accepted skips inside a passing
mini suite. Desktop full is already defined as mini plus the native-window
journey; the mobile full entries below require physical devices.

- Add physical-device Android full qualification, including the packaged
  arm64 TrustTunnel bridge.
- Add physical-device iOS full qualification for NetworkExtension consent,
  tunnel traffic, routing, disconnect, and cleanup.
- Add suspend/resume recovery scenarios on the platforms where the operating
  system lifecycle can be controlled reliably.
- Restore the diagnostic-only `functional.network-transition` scenario after
  each runner has a reversible transition that cannot cut off its test-control
  path.

The canonical mini/full platform model and semantic scenario membership remain
in the [functional contract](../torturer/docs/contract.md). Do not make a
deferred item pass through an expected-unavailable allowance.
