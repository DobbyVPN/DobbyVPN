# iOS Simulator checks

The public GitHub Test workflow prepares the Go and KMP frameworks and runs
the KMP Simulator tests. It then invokes `run_app_contract.py`, which checks
Metal availability, builds and launches the normal app, and waits for the
main view to attach. This is an app-startup check, not proof that Metal
presented a frame. The CLI takes only the checkout root and a temporary work
directory. It does not claim or validate source provenance.

The private Harness uses `--platform ios-simulator --simulator-mode mini` on a
host without usable Metal, or `--simulator-mode metal` on a Metal-capable host.
Its local adapter prepares Go/KMP frameworks and invokes the shared contract
directly; it does not call the public CLI. Mini builds and launches a
disposable app and requires its `startup.initialized mode=mini` marker without
constructing the Metal-backed Compose view. Metal requires a usable device and
the normal app's `startup.ui_attached mode=normal` marker. Neither mode checks
rendered pixels, accessibility exposure, or VPN traffic. Metal fails clearly
on a host without usable Metal and never falls back to Mini.

The app-group log is required for the startup marker. Copying log tails into
diagnostics is best-effort and is not a second validation step. The check
clears the disposable Simulator's app log before launch so a retained marker
from an earlier run cannot pass the check.

Neither Simulator mode tests VPN traffic or the physical-device-only
TrustTunnel bridge.
