# iOS Simulator mini check

The public GitHub Test workflow builds the Go packet-tunnel runtime
XCFramework, packages the Go/Fyne application with the native Swift
NetworkExtension shell, and installs that app on an iPhone Simulator.
`run_app_contract.py` then lets the XCTest target own the app launch and runs
one comprehensive UI mini contract. The
tests locate the real Fyne controls through the Simulator accessibility tree,
verify Settings/Back and release metadata, exercise real keyboard input and
editing/clearing by focusing the freshly rendered Fyne input once per edit and
tapping the published software-keyboard key frames, check empty and malformed
configuration outcomes, exercise reopen/persistence behavior, and exercise
the production Clear logs and native log export/share cancellation paths. The
exporter first presents an app-owned UIKit `Export logs` prompt with explicit
Share, Save, and Close actions. Share continues to `UIActivityViewController`;
Save uses `UIDocumentPickerViewController` to export the real compressed
archive. Both use the existing active app window's topmost presenter, including
the normal iPad popover anchor, and no transparent helper window is created.
Each keyboard/key lookup has a bounded wait; a disappeared keyboard or a
stale key subtree after three short retries causes the rendered input to be
refocused. The app-owned log is retained for diagnostics
only, and a startup marker is not a pass condition.

There is one Simulator mini contract. It uses the OpenGLES-backed Fyne package
and does not require an Apple Development certificate or a Metal capability
probe. A missing Simulator, build, XCTest UI target, app launch, accessibility
action, or expected visible outcome is a failed or unavailable check, never a
pass. Stage-specific timeouts identify whether inventory, boot, install, XCTest
(including its app launch), or cleanup failed; no stage is retried blindly.
An erased iOS 26 Simulator may spend several minutes in its normal Data
Migration, so the bootstatus stage has a six-minute bounded allowance within
the existing lane deadline and cleanup reserve.

The Simulator build writes its native log to the app-owned temporary directory
because a provisioning-free bundle cannot receive an App Group container. The
contract resolves that directory through `simctl get_app_container ... data`.
Copying bounded log tails into diagnostics is best-effort and does not decide
pass/fail. The check clears the disposable Simulator's app log before launch
so diagnostics from an earlier run cannot be mistaken for current evidence.
Physical iOS builds keep using the real App Group boundary.

The Simulator does not run a packet tunnel or the physical-device-only
TrustTunnel bridge. A signed physical-device build still requires the normal
Apple distribution certificate and provisioning profiles; that requirement is
separate from unsigned/ad-hoc Simulator packaging. A passing mini result is
therefore UI/lifecycle qualification only, not physical iOS VPN qualification.
