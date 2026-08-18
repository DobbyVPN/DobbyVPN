---
name: add-new-protocol
description: Add a protocol engine through the neutral Go runtime while preserving the shared UI and thin platform shells.
---

# Add a new protocol

This guide is intentionally protocol-neutral. DobbyVPN has one shared Compose
UI, one Go product/runtime layer, and thin operating-system shells only where
the platform VPN API requires them. A new protocol must not create a second
session manager, protocol-specific RPC, KMP repository, Swift lifecycle owner,
or UI toggle.

## Required implementation sequence

1. Add one configuration section and a safe profile summary in Go. Keep raw
   configuration, URLs, endpoints, credentials, and authentication metadata
   inside the Go boundary.
2. Implement the neutral `core/pkg.ProtocolDevice` interface for the engine:
   open/start, proxy-address (when applicable), server identity, and close.
   Make cancellation, startup failure, and reverse-order cleanup explicit.
3. Register one factory in the Go runtime composition root and route all
   control through SessionV2. Do not add a protocol-specific RPC or lifecycle
   export; desktop, Android, and iOS all use the existing SessionV2 contract.
4. Add parser, runtime, cleanup, and integration tests, including cancellation,
   failed startup, stale callbacks, and reconnect/recovery behavior.
5. Update the supported-protocol documentation and sanitized examples. Keep
   the shared Compose presentation flow unchanged.
6. Add matching SessionV2, Harness, and Torturer contract coverage only after
   the application behavior is complete. Preserve the existing test suite and
   its evidence norms.

## Platform boundary checklist

- Desktop shells provide authenticated local transport, service installation,
  and local diagnostics only.
- Android owns VPN permission, foreground-service lifetime, TUN allocation,
  socket protection, and native callback publication.
- iOS owns NetworkExtension/Packet Tunnel lifetime, TUN/socket callbacks, and
  native callback publication.
- Shared Compose code renders safe SessionV2 snapshots/events and never parses
  protocol configuration or owns VPN resources.

Before merging, prove that SessionV2 remains the sole externally meaningful
session/state/generation owner, that the Go module graph contains only the
intended engine dependencies, and that no per-protocol lifecycle path or
credential-bearing diagnostic output was introduced.
