# DobbyVPN architecture

## Architecture driver

> One shared UI layer where sharing is valuable, one Go product/runtime layer
> for behavior, and only thin OS-specific shells where VPN APIs require them.

This is the governing constraint for product changes. v1.5.0 simplifies
ownership and lifecycle; it does not remove the desktop GUI or create separate
protocol implementations in each platform UI.

## Responsibilities

### Shared Compose UI

The common Compose code owns presentation state, user interaction, navigation,
safe profile summaries, and rendering of SessionV2 snapshots and ordered
events. It stores only the accepted connection URL. It does not parse VPN
configuration, select a protocol, own a tunnel, poll service liveness, or
decide whether a platform VPN is connected.

The same UI and presentation logic is used on Linux, Windows, macOS, Android,
and iOS wherever the platform build supports that target. A platform shell may
provide an adapter, but it must not fork the product flow.

### Go product/runtime

Go owns configuration acquisition and parsing, profile policy and selection,
SessionV2 command idempotency, session and generation state, protocol-device
construction, probes, routing/TUN/tun2socks resources, cleanup, diagnostics,
and the native desktop CLI.

Configuration enters Go as one HTTPS/HTTP URL or transient inline bytes. Go
returns only safe summaries, digests, source kind, typed warnings, and typed
failures. URLs, raw configuration, endpoints, credentials, and authentication
metadata never cross a UI, gRPC, mobile-binding, or diagnostics boundary.

SessionV2 is the sole externally meaningful session and generation owner. It
supports capabilities, create, recover, configure, start, stop, snapshot,
ordered watch events, and destroy. A service owns at most one recoverable
session. A UI restart recovers that session and resumes events after the last
accepted sequence; it does not create a competing tunnel.

Supported configuration sections are Outline, Outline WebSocket, Xray, and
TrustTunnel. Cloak is removed: a Cloak-bearing section is rejected with the
safe typed `UNSUPPORTED` failure, without returning any source fields.

### Thin platform shells

Platform code owns only operating-system responsibilities that Go cannot own:

- Android: VPN permission, foreground `VpnService` lifetime, TUN allocation,
  socket protection, and publication of native state to shared code.
- iOS: NetworkExtension/Packet Tunnel lifetime, TUN/socket callbacks, and
  publication of native state to shared code.
- Desktop: authenticated local control transport, service installation/start,
  and local diagnostics. The desktop shell does not own protocol-specific
  lifecycle or configuration parsing.

The shells do not introduce per-protocol start/stop RPCs, separate session
managers, or UI-specific configuration repositories.

## Control flow

```text
Shared Compose UI
    -> protocol-neutral SessionV2 client
        -> Go SessionV2 manager (sole policy/state owner)
            -> Go runtime lease
                -> Outline | Xray | TrustTunnel device
                -> routing/TUN/tun2socks/probe resources

Android shell: permission + foreground service + TUN/socket callbacks
iOS shell: NetworkExtension lifecycle + TUN/socket callbacks
Desktop shell: authenticated transport + local diagnostics
```

Session events are push-based. Desktop uses the authenticated gRPC `Watch`
stream; Android and iOS use the generation-correlated native callback exposed
to shared code as a flow. Clients apply a snapshot first, then accept events
strictly after its sequence. Duplicate events are ignored; a sequence gap
causes snapshot/resubscribe rather than inferred state. No UI path polls on a
timer.

An explicit Disconnect stops and destroys the session. Closing or crashing a
desktop GUI, or swiping an Android task, is UI loss only: a healthy service
session remains active and a reopened UI recovers it. If the service/process
dies, the next launch must reconcile stale Dobby-owned resources and never
show a false `CONNECTED` state.

## Adding a future protocol

Do not add a protocol-specific RPC, KMP repository, Swift lifecycle owner, UI
toggle, or platform-specific configuration parser. Use this checklist:

1. Add one Go configuration section and a safe profile summary.
2. Implement the neutral `ProtocolDevice` interface.
3. Register one factory in the Go native composition root.
4. Add parser, runtime, cleanup, and integration tests, including failure and
   cancellation paths.
5. Update the supported-protocol list and sanitized examples.
6. Add the matching SessionV2/Harness/Torturer contract coverage only after
   the application behavior is complete.

The new implementation must preserve one SessionV2 state owner, reverse-order
resource cleanup, source redaction, and the shared Compose UI.
