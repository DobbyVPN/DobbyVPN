# iOS Simulator checks

The public GitHub Test workflow prepares the Go and KMP frameworks and runs
the KMP Simulator tests. It then invokes `run_app_contract.py` for one Metal
capability check and one Xcode UI-test run. XCTest builds and launches the app
for that UI smoke; the workflow does not separately build the app. The CLI
takes only the checkout root and a temporary work directory. It does not claim
or validate source provenance.

The private Harness uses `--platform ios-simulator --simulator-mode mini` on a
host without usable Metal, or `--simulator-mode metal` on a Metal-capable host.
Its local adapter prepares Go/KMP frameworks and invokes the shared contract
directly; it does not call the public CLI. Mini builds and launches a
disposable app and requires its `startup.initialized mode=mini` marker without
UI rendering or VPN traffic. Metal runs one `xcodebuild test` command, which
builds and runs the UI smoke. It fails clearly when the host has no usable
Metal device and never falls back to Mini.

App-group logs are retained best-effort when present; they are diagnostic only
and do not participate in a second validation or attestation step.

Neither Simulator mode tests VPN traffic or the physical-device-only
TrustTunnel bridge.
