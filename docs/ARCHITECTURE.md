# DobbyVPN architecture

DobbyVPN keeps product behavior in Go, shares the Compose UI across platforms,
and uses small platform shells only where an operating system requires VPN
APIs.

## Ownership

Go owns configuration loading and parsing, profile inventory and automatic
protocol selection, probing and failover, protocol runtimes, session state, and
resource cleanup. Outline, including its WebSocket variant, and Xray are
supported on all current platforms.
TrustTunnel remains supported on production targets; its native dependency is
unavailable in Android x86_64 and iOS Simulator builds.

Each Go service process owns one session manager. Clients attach with
`Snapshot`; `Watch` sends the current complete snapshot followed by the latest
complete snapshot after changes. Slow consumers can skip intermediate
revisions. `ValidateConfig` is stateless. `Configure` and `Start` require the
revision returned by a snapshot; `Stop` is fenced by generation. `Reset` clears
accepted configuration after cleanup. No client needs to create, recover,
destroy, or replay sessions.

The desktop CLI and desktop GUI use the same authenticated gRPC service. The
CLI is implemented in Go and does not start a JVM. Mobile bindings expose the
same manager through a small JSON boundary. Kotlin/Compose remains on the
current JVM build for desktop; Kotlin/Native migration is not part of this
cleanup.

## Platform shells

- Android owns VPN permission, foreground `VpnService` lifetime, TUN
  allocation, and socket protection. Its Go callback wakes the app, which then
  reads a fresh snapshot.
- iOS owns NetworkExtension lifetime, tunnel settings, and the app/provider
  command handoff. The app stores raw configuration in an encrypted one-shot
  Keychain mailbox; configuration bytes do not enter provider messages. Go
  owns session state in the provider process. Darwin notifications carry only
  wake hints, followed by a current snapshot read.
- Desktop owns authenticated local transport, service installation/start, and
  local diagnostics. It does not choose protocol policy or parse
  configuration.

The platform shells do not create separate protocol implementations or
session managers.

## Configuration and failures

Go accepts an HTTP(S) source URL or transient inline configuration. UI code
persists only a URL after configuration succeeds. Returned profiles omit
dedicated server-address fields and protocol payloads. The user-provided
Description is returned unchanged and may itself contain sensitive text.
Public failure messages use typed, input-safe text; raw URLs, credentials, and
configuration are not echoed in failure responses or logs.

An unsupported legacy section such as Cloak rejects the whole configuration
with `UNSUPPORTED`. Supported sections in the same input are not partially
started.

## Adding a protocol

Add a Go parser section and profile summary, implement the shared runtime
interface, register it in the Go composition root, and cover probing, cleanup,
failure, and cancellation. Update the supported-protocol list and sanitized
examples. Add UI or per-platform protocol controls only for a concrete product
requirement.
