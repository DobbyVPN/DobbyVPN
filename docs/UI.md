# UI behavior

The shared Go/Fyne UI renders the state owned by one Go session manager in each
service process. Desktop, Android, and iOS release packages launch this native
UI directly; no package carries a JVM or Compose UI runtime. It forwards the
entered source bytes and displays current snapshots. It does not parse
configuration, choose a protocol, or manage tunnel resources. Android and iOS
retain thin native VPN shells for permission, foreground/extension lifetime,
secure storage, and system sharing APIs. Those shells must not duplicate
session or protocol policy.

## UI qualification

The desktop test companion (`desktop_build.py ui-test`) embeds the same
production widgets and Fyne's headless driver. Its configure request stores
the profile through the real UI client, then Connect, Disconnect, and
reconnect actions exercise the production widget callbacks against the
authenticated gRPC service. The shared functional adapter delegates tunnel,
routing, traffic, process-loss, and cleanup assertions to the normal platform
adapter. A Windows/macOS native-window qualification also starts the packaged
binary, discovers controls through the platform accessibility tree, enters a
fresh profile through native clipboard/keyboard input, clicks Connect and
Disconnect, observes visible status, opens Settings, and verifies close/reopen
reattaches to the service-owned session. A missing interactive desktop or
denied automation permission is an incomplete/failed GUI lane, not a pass.
Linux deliberately stays CLI/service-only.

The Fyne driver is not an operating-system input simulator: it validates
widget callbacks, layout state, accessibility labels, and the real service
boundary. The native-window qualification covers actual rendering and input
wiring; Android UI Automator and iOS Simulator accessibility checks cover
mobile rendered controls. Neither duplicates the canonical functional scenario
definitions.

## Connect and reattach

At startup, the UI attaches with `Snapshot` and starts `Watch`. `Watch` sends
the current snapshot first, then the latest snapshot after each change; slow
clients may skip intermediate revisions and always render the complete newest
state. Android and iOS use native callbacks only as wake hints and read a fresh
Go snapshot after each wake.

Connect calls `Configure` with the entered HTTPS URL or transient inline
configuration. Go fetches and validates the source before accepting it. URL
downloads must stay on HTTPS across redirects, and downloaded and inline
configuration are limited to 1 MiB. Fetching occurs before the new tunnel is
up. The UI stores a URL only after Go accepts it, and does not log the entered
source. A subsequent `Start` includes the current snapshot revision so a stale
UI cannot start from an outdated state. Go selects profiles, probes protocols,
performs automatic failover, and owns runtime cleanup.

Supported profiles remain Outline (including its WebSocket variant), Xray,
and TrustTunnel. Only those profile sections and optional `ExcludeIPs` are
accepted at the configuration root. Any other root section or key rejects the
full configuration with a typed, input-safe failure; Go does not partially
start supported sections from that input. TrustTunnel certificate verification
must remain enabled; `skip_verification = true` is rejected.

TrustTunnel's native bridge is available on physical Android arm64 and iOS
devices. Android x86_64 and iOS Simulator builds report a TrustTunnel-specific
runtime failure because the bridge is not packaged for those targets. The UI
shows that failure message with its diagnostic code.

The GUI always uses automatic selection. The CLI's `connect-profile` command
and the functional test harness use the same runtime's operator/test mode to
select a specific profile index. It skips automatic selection and does not
switch profiles after a health failure.

Automatic selection may attempt recovery up to three times after health
failures. A fourth failure before five uninterrupted connected minutes ends
the session in failure and requires the user to connect again. Five stable
connected minutes reset that allowance. While a recovery is underway, snapshots
carry `recovering = true`, including the temporary `IDLE` state during cleanup;
the UI presents this as “Reconnecting” so teardown does not flash as a normal
disconnect. The UI also presents the active protocol, supplied warnings, and
the failure message with its code available as diagnostic detail.

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
- The shared Go/Fyne UI maps snapshots to presentation state. It does not maintain an
  event ledger or synthesize connection state. It does not run its own polling
  loop on desktop; the mobile adapter uses a bounded foreground-only refresh
  while the native service/extension remains the lifecycle owner.
- Diagnostics stay local unless the user exports them. Android and iOS create a
  compressed file and open the platform share sheet; desktop opens a save
  dialog. DobbyVPN does not receive the exported logs automatically.
