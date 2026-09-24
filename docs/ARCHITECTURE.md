# DobbyVPN architecture

DobbyVPN shares connection behavior in Go and uses native frontends for each
supported desktop and mobile platform. Linux remains a Go backend and CLI
without a GUI.

## Ownership

The Go backend owns configuration loading and parsing, profile inventory,
automatic protocol selection and recovery, session and generation state,
protocol runtimes, probing, and resource cleanup. Outline, Xray, and
TrustTunnel remain supported. Xray's gRPC transport is part of that VPN
protocol support; desktop control uses a separate local JSON interface.

Native code owns presentation and operating-system VPN integration. It does
not implement profile selection, probing, recovery, or another session manager.

| Platform | Frontend and native boundary |
| --- | --- |
| Windows | WinUI 3 in C#; the Windows service runs the Go backend |
| macOS | SwiftUI; launchd runs the Go backend |
| Android | Jetpack Compose and a thin Kotlin VpnService/JNI boundary |
| iOS | SwiftUI and a thin NetworkExtension boundary around the Go runtime |
| Linux | Go backend and CLI only |

## Desktop control

The CLI and desktop frontends call the same Go backend. The frontend sends one
JSON request per local connection. The supported calls are Snapshot,
Configure, Start, and Stop. Frontends poll Snapshot for current state. Every
change remains fenced by the Go session ID, sequence, or generation.

macOS and Linux use a Unix domain socket with local peer checks. Windows uses
the fixed DobbyVPN.Control named pipe. Its access list grants the installed
interactive user and SYSTEM; the pipe rejects remote clients. No UI action
starts a CLI subprocess.

## Configuration and diagnostics

Go persists an accepted HTTPS configuration URL and returns it in Snapshot so
frontends can restore it. Existing desktop URLs are migrated into the
backend-owned store. Inline configuration is kept only for the current
connection session and is not written to the saved-URL store. Using inline
configuration leaves the last saved URL intact; Reset clears it.

Go fetches URL configurations only over HTTPS, including across redirects.
Downloaded and inline configurations are limited to 1 MiB. Configuration
parsing and profile decisions remain in Go.

Native frontends read fixed local diagnostic files directly and display their
contents as written. Product logs are not sanitized. The private Harness and
hosted workflows still redact credentials and private profile values when
forwarding command output.

## Build and test ownership

Go modules and the platform build entrypoints live in this repository.
Android's standalone Gradle project builds the Go library for its two packaged
ABIs. iOS builds the Go NetworkExtension runtime XCFramework and links it into
the Swift application. Windows and macOS desktop frontends are built on their
native target hosts.

The functional scenarios and pass criteria are owned by
[torturer/docs/contract.md](../torturer/docs/contract.md). Supported checks
and their commands are listed in [TESTING.md](../TESTING.md).
