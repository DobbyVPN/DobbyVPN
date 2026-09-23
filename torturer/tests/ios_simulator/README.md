# iOS Simulator mini check

The public GitHub Test workflow's `ios_simulator_mini` job downloads the
already-built Go packet-tunnel runtime XCFramework from the `ios_libraries`
job, validates its iOS-Simulator metadata and required host architecture,
packages the Go/Fyne application with the native Swift NetworkExtension shell,
and installs that app on an iPhone Simulator.
`run_app_contract.py` then lets the XCTest target own the app launch and runs
one comprehensive UI mini contract. The
tests locate the real Fyne controls through the Simulator accessibility tree,
verify Settings/Back and release metadata, exercise real keyboard input and
editing/clearing by focusing the freshly rendered Fyne input once per edit and
tapping the published software-keyboard key frames, check local empty-input
validation, submit a visibly non-empty value, exercise reopen/persistence
behavior, and use the production Clear logs plus the supported native
log-export flow: the
app-owned UIKit `Export logs` prompt exposes explicit Share, Save, and Close
actions, then Save is canceled in the native document picker. The simulator
does not require the follow-on `UIActivityViewController` surface, which can be
suspended by a headless iOS 26 runtime. Save uses
`UIDocumentPickerViewController` to export the real compressed archive through
the existing active app window's topmost presenter, including the normal iPad
popover anchor; no transparent helper window is created.
Each keyboard/key lookup has a bounded wait; a disappeared keyboard or a
stale key subtree after three short retries causes the rendered input to be
refocused. App output remains ephemeral, and a startup marker is not a pass
condition.

There is one Simulator mini contract. It uses the OpenGLES-backed Fyne package
and does not require an Apple Development certificate or a Metal capability
probe. A local invocation without `--runtime-framework` still builds the
pinned Simulator XCFramework; hosted invocation supplies the downloaded
artifact explicitly and never rebuilds it. A missing Simulator, build, XCTest UI target, app launch, accessibility
action, or expected visible outcome is a failed or unavailable check, never a
pass. Stage-specific timeouts identify whether inventory, boot, install, XCTest
(including its app launch), or cleanup failed; no stage is retried blindly.
The package script runs the same shared XCFramework metadata, containment, and
Mach-O slice validator for both Simulator and physical-iOS packaging lanes.
An erased iOS 26 Simulator may spend several minutes in its normal Data
Migration, so the bootstatus stage has a six-minute bounded allowance within
the existing lane deadline and cleanup reserve.

The Simulator contract does not resolve or copy app containers, log tails, or
diagnostic archives. Its visible Clear/Export interactions validate the
production UI wiring, while the disposable Simulator and run directory own
all temporary output. Physical iOS builds keep using the real App Group
boundary.

The Simulator does not run a packet tunnel or the physical-device-only
TrustTunnel bridge. A signed physical-device build still requires the normal
Apple distribution certificate and provisioning profiles; that requirement is
separate from unsigned/ad-hoc Simulator packaging. A passing mini result is
therefore UI/lifecycle qualification only, not physical iOS VPN qualification.
