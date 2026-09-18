# iOS Simulator checks

The public GitHub Test workflow builds the Go packet-tunnel runtime
XCFramework, packages the Go/Fyne application with the native Swift
NetworkExtension shell, and launches that same app on an iPhone Simulator.
`run_app_contract.py` checks the app-owned startup and UI-attached markers,
not VPN traffic or screenshots.

The private Harness accepts `--platform ios-simulator --simulator-mode mini`
on a host without usable Metal and `--simulator-mode metal` on a Metal-capable
host for compatibility with existing commands. Both modes now exercise the
same OpenGLES-backed Fyne package; neither requires an Apple Development
certificate, and neither performs a Metal capability probe. The distinction is
only the caller's host/timeout policy. A missing Simulator, build, launch,
accessibility permission, or startup marker is a failed or unavailable check,
never a pass.

The Simulator build writes its native log to the app-owned temporary directory
because a provisioning-free bundle cannot receive an App Group container. The
contract resolves that directory through `simctl get_app_container ... data`.
Copying bounded log tails into diagnostics is best-effort and does not decide
pass/fail. The check clears the disposable Simulator's app log before launch
so an earlier marker cannot satisfy the current run. Physical iOS builds keep
using the real App Group boundary.

The Simulator does not run a packet tunnel or the physical-device-only
TrustTunnel bridge. A signed physical-device build still requires the normal
Apple distribution certificate and provisioning profiles; that requirement is
separate from unsigned/ad-hoc Simulator packaging.
