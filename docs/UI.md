# UI behavior

The shared Compose UI renders the state owned by one Go session manager in each
service process. It forwards the entered source bytes, requests VPN permission,
and displays current snapshots. It does not parse configuration, choose a
protocol, or manage tunnel resources.

## Connect and reattach

At startup, the UI attaches with `Snapshot` and starts `Watch`. `Watch` sends
the current snapshot first, then the latest snapshot after each change; slow
clients may skip intermediate revisions and always render the complete newest
state. Android and iOS use native callbacks only as wake hints and read a fresh
Go snapshot after each wake.

Connect calls `Configure` with the entered HTTP(S) URL or transient inline
configuration. Go fetches and validates the source before accepting it. The UI
stores a URL only after Go accepts it, and does not log the entered source. A
subsequent `Start` includes the current snapshot revision so a stale UI cannot
start from an outdated state. Go selects profiles, probes protocols, performs
automatic failover, and owns runtime cleanup.

Supported profiles remain Outline (including its WebSocket variant), Xray,
and TrustTunnel. An unsupported legacy section such as Cloak rejects the full
configuration with a typed, input-safe failure; Go does not partially start
the supported sections from that input.

## Disconnect and cleanup

Disconnect sends `Stop` for the active generation. Go releases that
generation's resources and publishes the complete current snapshot. The
accepted configuration remains available for reconnect. A later `Configure`
can replace it after cleanup completes. `Reset` clears the accepted
configuration after successful cleanup when a caller needs an empty session.

Closing the desktop GUI or swiping away the Android UI does not stop a healthy
service-owned tunnel. Reopening the UI attaches to the same process-owned
session. If that service process ends, its Go owner ends with it; a new owner
starts in `IDLE`, and no client replays stale local state.

On iOS, the containing app sends opaque control commands to the Network
Extension provider. Raw configuration passes through a one-shot encrypted
Keychain mailbox and never enters the app message. Darwin notifications carry
no state; the app follows each wake with a current Go snapshot.

## Ownership

- Go owns configuration acquisition and parsing, profile selection, automatic
  failover, protocol runtimes, and session state.
- Platform shells own only VPN permission, foreground/extension lifetime,
  tunnel creation, socket protection, and wake delivery.
- The shared UI maps snapshots to presentation state. It does not maintain an
  event ledger or synthesize connection state. It does not run its own polling
  loop; the iOS bridge refreshes a snapshot on each Darwin wake or after its
  five-second wait timeout.
- Diagnostics stay local. Remote telemetry is not part of the session flow.
