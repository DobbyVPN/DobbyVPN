# Codex Agent Instructions

## Source Search

Use `colgrep` as the primary search tool for source-code exploration in this
repository.

Examples:

```bash
colgrep "iOS packet tunnel lifecycle" . -k 20 -n 6
colgrep -e "SetMemoryLimit" "Go memory tuning" . -k 20 -n 6
colgrep --include="*.swift" "VPN manager stop tunnel" . -k 20
```

If `colgrep` is not initialized or reports that it cannot search the project,
initialize it when possible. If initialization cannot be completed locally, stop
and ask the user to initialize `colgrep` before continuing with source-code
analysis. Exact text searches in plain log files may use `rg`.

## Project Purpose

DobbyVPN is a cross-platform VPN client for iOS, Android, Windows, macOS, and
Linux. The app currently wraps OutlineSDK and Cloak, with XRay and AWG in
progress, as described in `README.md`.

The long-term purpose is to support many VPN protocols and allow protocol
changes during runtime in a way that is transparent to the user.

## Architecture Constraints

`tun2socks` is in use and must remain part of the architecture. Do not propose
or implement removing it as a default direction. The client's main goal is to
support multiple VPN protocols behind one user-facing VPN experience, and
`tun2socks` is part of that protocol-agnostic routing model.

When changing protocol or tunnel code, preserve the ability to switch and route
between protocols without exposing protocol-specific complexity to the user.

## Engineering Priorities

Simplicity is a key factor. Prefer straightforward, observable designs over
large abstractions or speculative rewrites.

Logging is a key feature of the application. The app intentionally uses
extensive logs to understand and troubleshoot runtime behavior across platforms.
When adding or changing behavior, keep logs clear, specific, and useful for
post-failure analysis.

Avoid misleading logs. A log message should say exactly what is known. For
example, do not label a stop as user-initiated unless the code has direct
evidence that the user clicked the stop control.
