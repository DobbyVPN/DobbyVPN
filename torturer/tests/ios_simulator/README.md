# iOS Simulator mini check

The CI iOS Simulator job builds the Go packet-tunnel runtime XCFramework,
packages the SwiftUI app, installs it in an iOS Simulator, and runs this XCTest
UI contract. The local Harness runs the same contract through its iOS
Simulator mini check.
The contract checks that the app renders, accepts input, validates an empty
source, reports a visible connection failure for a non-empty invalid source,
and survives terminate/reopen lifecycle actions.

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
