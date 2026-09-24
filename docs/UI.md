# UI behavior

This page describes the current Go/Fyne UI and its tests. The agreed native UI
replacement has not been implemented yet.

The shared Go/Fyne UI renders the state owned by one Go session manager in each
service process. Desktop, Android, and iOS release packages launch this native
UI directly; desktop carries no JVM launcher, and no platform carries a KMP or
Compose UI runtime. Android's thin Kotlin/Java boundary runs on ART for
permission and `VpnService` lifetime; iOS retains a thin Swift
NetworkExtension boundary. The Go UI forwards entered source bytes and
displays current snapshots. It does not parse configuration, choose a
protocol, or manage tunnel resources. Android and iOS retain thin native VPN
shells for permission, foreground/extension lifetime, secure storage, and
system sharing APIs. Those shells must not duplicate session or protocol
policy.

## UI qualification

The desktop test companion (`desktop_build.py ui-test`) embeds the same
production widgets and Fyne's headless driver. Its configure request stores
the profile through the real UI client, then Connect, Disconnect, and
reconnect actions exercise the production widget callbacks against the
authenticated gRPC service. The shared functional adapter delegates tunnel,
routing, traffic, process-loss, and cleanup assertions to the normal platform
adapter. A Windows/macOS native-window qualification starts the native binary
(exact-Release mode uses the installed Release package), discovers controls
through the platform accessibility tree, enters a fresh profile through native
clipboard/keyboard input, clicks the stable connection action, observes visible
status, opens Settings, and verifies close/reopen reattaches to the service-owned
session. A missing interactive desktop or
denied automation permission is an incomplete/failed GUI lane, not a pass.
Linux deliberately stays CLI/service-only.

The Fyne driver is not an operating-system input simulator: it validates
widget callbacks, layout state, accessibility labels, and the real service
boundary. The native-window qualification covers actual rendering and input
wiring; Android UI Automator and iOS Simulator accessibility checks cover
mobile rendered controls. Neither duplicates the canonical functional scenario
definitions.

The connection editor keeps large multiline inline pastes outside Fyne's
RichText renderer: it shows the fixed non-secret `Inline configuration ready`
summary while retaining the exact source privately until Connect submits it.
One-line HTTPS URLs and ordinary edits retain normal Entry behavior; the Logs
editor is not staged.

On Windows and macOS, the native window title mirrors the global rendered
connection status (`Dobby VPN — <status>`), including while Settings is open.
The connection action keeps one stable accessibility label while its visible
text changes between Connect and Disconnect. Fyne 2.8.1's Darwin child-label
snapshots do not reliably republish dynamic text, so native drivers use the
exact-PID window title for live status and the stable action label for physical
input. Settings owns a genuinely new content tree without changing the VPN
status channel. This stays within the public Fyne API and requires no fork or
upstream change.

## Connect and reattach

At startup, the UI attaches with `Snapshot` and starts `Watch`. `Watch` sends
the current snapshot first, then the latest snapshot after each change; slow
clients may skip intermediate revisions and always render the complete newest
state. Android and iOS read a fresh Go snapshot on a bounded 500 ms loop while
the foreground view is attached.

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
Keychain mailbox and never enters the app message. The foreground Go client
polls current snapshots; Swift does not maintain a second event stream.

## Ownership

- Go owns configuration acquisition and parsing, profile selection, automatic
  failover, protocol runtimes, and session state.
- Platform shells own only VPN permission, foreground/extension lifetime,
  tunnel creation, socket protection, and provider-message delivery.
- The shared Go/Fyne UI maps snapshots to presentation state. It does not maintain an
  event ledger or synthesize connection state. While the view is attached, it
  refreshes retained diagnostics on a bounded 500 ms loop; the native
  service/extension remains the lifecycle owner.
- Diagnostics stay local unless the user exports them. Android and iOS create a
  compressed file and open the platform share sheet; desktop opens a save
  dialog. DobbyVPN does not receive the exported logs automatically.
