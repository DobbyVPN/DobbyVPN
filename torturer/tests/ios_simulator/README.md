# iOS Simulator mini check

The CI iOS Simulator job builds the Go packet-tunnel runtime XCFramework,
packages the SwiftUI app, installs it in an iOS Simulator, and runs this XCTest
UI contract. The local Harness runs the same contract through its iOS
Simulator mini check.
The contract checks that the app renders, accepts a non-empty draft, and
retains that draft across tab navigation. It activates the connection action
and checks that a visible connection error appears. It then edits the draft to
`invalidprofile2` and verifies that value immediately and after navigating
away and back. It also checks release metadata, the Logs screen, and app
terminate/relaunch behavior.

The connection error is independent of the editable draft. The Simulator may
report a NetworkExtension provider IPC failure, and editing the draft is not
expected to clear that error.

The test interacts with the real SwiftUI accessibility elements. It does not
start a physical NetworkExtension tunnel, validate VPN traffic, or test the
TrustTunnel bridge. Physical-device VPN coverage requires a physical iOS
runner.

Simulator packaging uses an ad-hoc signature and does not require an Apple
development certificate. Build and test output belongs to the current
disposable test run; this contract does not retain a separate log or evidence
archive.

The shared platform coverage and result semantics are defined in
[the functional contract](../../docs/contract.md).
