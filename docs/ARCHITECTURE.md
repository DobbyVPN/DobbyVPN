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
- iOS: NetworkExtension/Packet Tunnel lifetime, TUN/socket callbacks, and the
  authenticated opaque command handoff between the app and provider. The
  provider starts in control mode without routes. Go enters the generation
  lifecycle first; its `AcquireTunnel` callback is the point where the
  provider applies fixed tunnel settings immediately before duplicating the
  requested TUN. A one-shot encrypted Keychain mailbox carries raw
  configuration; no configuration bytes enter an app message.
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
iOS shell: NetworkExtension lifecycle + authenticated control messages + TUN/socket callbacks
Desktop shell: authenticated transport + local diagnostics
```

Session events remain Go-owned and ordered. Desktop uses the authenticated gRPC
`Watch` stream; Android uses the generation-correlated native callback. iOS
uses a content-free provider wake signal and fetches the real event ledger via
authenticated Go `Observe`; the iOS KMP bridge waits for the cross-process
wake and performs those reads off the UI dispatcher. Clients apply a snapshot first, then accept events
strictly after its sequence. Duplicate events are ignored; a sequence gap
causes snapshot/resubscribe rather than inferred state. No UI path owns a
timer or manufactures an event.

### iOS command-boundary exception

NetworkExtension places the Go runtime in a separate packet-tunnel process, so
iOS has one narrow opaque handoff exception: the containing app creates or
recovers the Go session by sending authenticated fixed commands through
`NETunnelProviderSession.sendProviderMessage`. The per-install Keychain HMAC
secret authenticates canonical envelopes with bounded size and request IDs.
Provider replies are likewise authenticated envelopes containing the request ID
and the exact Go response bytes (base64 inside the envelope); the app validates
the wrapper and returns the inner Go JSON unchanged. Swift retains only its
NetworkExtension readiness/request fence; SessionV2 remains the sole owner of
configuration, generation, state, event sequence, and cleanup.

An ordinary Disconnect sends Go `Stop`: it releases the active generation and
its runtime resources while retaining the accepted configuration and
recoverable SessionV2 session for reconnect. It does not call `Destroy` or
stop the iOS control provider. `Destroy` is a separate terminal disposal path;
the app stops the control provider and consumes the one-shot mailbox only after
Go has returned a successful Destroy result. A failed or timed-out Destroy
retains recovery data. Closing or crashing a desktop GUI, or swiping an
Android task, is UI loss only: a healthy service session remains active and a
reopened UI recovers it. If the service/process dies, the next launch must
reconcile stale Dobby-owned resources and never show a false `CONNECTED` state.

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
