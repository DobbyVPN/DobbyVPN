---
name: add-new-protocol
description: Add a protocol engine to the shared Go backend used by native frontends.
---

# Add a new protocol

Protocol implementations belong in the shared Go backend. Native frontends use
the common session API and do not add protocol-specific UI, control transports,
or session managers. See [AGENTS.md](../../AGENTS.md) and
[docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md) for the implemented
architecture.

## Implementation sequence

1. Add configuration parsing and a profile summary in Go. Use synthetic
   committed fixtures.
2. Implement the protocol device lifecycle, including cancellation, startup
   failure, and reverse-order cleanup.
3. Register the protocol factory in the Go runtime and expose it through the
   existing `sessionapi`. Keep desktop JSON control and the mobile binding
   protocol-neutral.
4. Add focused parser, runtime, cleanup, and integration tests for success,
   cancellation, startup failure, and recovery.
5. Update supported-protocol documentation and synthetic examples. Product
   diagnostics are displayed as written; do not add log sanitization.
6. Add functional coverage through the existing shared scenarios and contract.
   Keep scenario definitions and pass criteria in `torturer/docs/contract.md`.

## Platform boundaries

- Windows and macOS frontends call the Go backend over the existing local JSON
  endpoint. Linux remains backend and CLI only.
- Android and iOS keep VPN permission and operating-system lifecycle work in
  their native boundaries, with protocol behavior in Go.
- The Go backend owns parsing, profile selection, session state, protocol
  runtimes, recovery, and cleanup.
- Native frontends read the fixed diagnostic files and display their contents
  without sanitization.

Before completion, run the Go and relevant functional checks. Confirm that all
supported frontends use the shared session API and that no protocol-specific
control path or duplicate session owner was added.
