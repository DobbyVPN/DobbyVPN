# UI behavior

The Compose UI is a shared presentation layer. It renders safe SessionV2
snapshots and push events; it does not parse configuration or own VPN
resources.

## Startup and reattachment

On startup the UI creates an authenticated, protocol-neutral SessionV2 client
and attempts `RecoverActiveSession`.

- If a live session exists, apply its safe snapshot and resume `Watch` after
  the returned sequence.
- If no session exists, render `DISCONNECTED` and wait for user input.
- If the service is unavailable, render a typed unavailable/error state; never
  infer `CONNECTED` from a stale local flag or platform-service liveness.

The UI does not run a timer-based connection detector. Android and iOS receive
the same ordered state model through the native callback/Flow boundary.

## Connect

The shared UI sends one source to Go:

1. Recover the active session, if any.
2. If no session exists, call `CreateSession`.
3. Call `Configure` with either the entered HTTP(S) URL or transient inline
   bytes.
4. Render safe profile summaries, digest, source kind, and typed warnings.
5. Call `Start` and render ordered generation events.
6. Persist only an accepted connection URL; never persist raw configuration or
   parsed profiles.

Go fetches and parses the configuration, selects supported protocols, probes
variants, and owns the runtime lease. A configuration containing a removed
legacy section such as Cloak is rejected in full with a safe typed `UNSUPPORTED`
result before profile selection or scenario execution; supported sections from
the same input are never partially started.

```text
Connect
  -> RecoverActiveSession or CreateSession
  -> Configure(URL | INLINE)
  -> safe snapshot/warnings
  -> Start
  -> ordered Watch/native callback events
```

If another live session exists, `CreateSession` returns typed `CONFLICT`; the
UI must not stop or replace that session implicitly. The UI instead recovers
and renders the existing session.

## Disconnect

Disconnect is explicit and owned by SessionV2:

```text
Disconnect
  -> Stop
  -> await terminal cleanup outcome
  -> Destroy
  -> render DISCONNECTED
```

Go releases protocol, routing, TUN, tun2socks, probe, DNS, and child-process
resources in reverse acquisition order. The UI reports a cleanup failure as a
failure; it does not silently render a clean disconnect.

## UI loss and recovery

- Closing or crashing a desktop GUI does not stop a healthy service-owned
  tunnel. Reopening the GUI recovers the same session and generation.
- Swiping an Android task removes only the UI. The foreground VPN service
  remains responsible for a healthy tunnel, and the reopened UI recovers it.
- If the service/process dies, OS TUN closure and the next startup
  reconciliation must yield `DISCONNECTED` with no stale resource owner.
- iOS follows the NetworkExtension lifecycle and uses the same shared state
  model; Simulator/build evidence is not physical packet-tunnel evidence.

## Safety rules

- No UI code parses protocol configuration or chooses a protocol.
- No UI code logs URLs, configuration, endpoints, credentials, or auth
  metadata.
- No UI code polls session state on a timer.
- No per-protocol start/stop path or platform-specific session manager may be
  added.
- Diagnostics are local-only. Remote telemetry initialization, persistence, and
  network upload are not part of the product lifecycle.
