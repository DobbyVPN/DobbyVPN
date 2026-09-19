# DobbyVPN architecture

DobbyVPN keeps product behavior and the shared UI in Go, and uses small
platform shells only where an operating system requires VPN APIs. Desktop,
Android, and iOS release packages render the UI with Fyne. Desktop packages
have no JVM launcher, and no platform uses a KMP/Compose UI. Android still
runs its thin Kotlin/Java OS boundary on ART for `VpnService`, permission,
foreground notification, and JNI plumbing. Swift remains in the iOS project
only for NetworkExtension, secure storage, app-group logging, and the C bridge
used by the Go/Fyne app. A mobile build is not complete until its rendered
controls, permission flow, and native lifecycle tests pass; a headless Fyne
test or a separately built renderer is not evidence of a working VPN client.

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

The GUI uses automatic profile selection. The CLI's `connect-profile` command
and the functional test harness can select a profile index through the same
session manager; this operator/test mode skips automatic selection and does
not fail over to a different profile.

The desktop CLI and native Go/Fyne GUI use the same authenticated gRPC service.
Neither starts a JVM. Android and iOS Go/Fyne packages use the same
`SessionClient` model through narrow JSON/native transport boundaries. The
native shells must not become a second owner of protocol policy or session
state.

## Platform shells

The Android app is the standalone `android_module` Gradle project. Its
`buildGoUI` task cross-compiles `cmd/dobbyui` directly to one shared library per
ABI and packages the pinned Fyne Java activity. iOS uses
`go_module/scripts/package_ios_app.sh`: `build_ios_xcframework.sh` produces the
Go packet-tunnel runtime XCFramework, while the packaging script builds the
small Swift CommonDI/tunnel frameworks and links the Go/Fyne executable. The
remaining `gomobile` invocation is only for the NetworkExtension runtime
XCFramework; it is not a Kotlin Multiplatform or Compose build. Simulator
packaging uses temporary ad-hoc signing metadata and does not require an Apple
Development certificate. Physical-device/App Store packaging still requires
the distribution certificate and provisioning profiles supplied by Release.

- Android owns VPN permission, foreground `VpnService` lifetime, TUN
  allocation, and socket protection. The Go/Fyne process calls a JNI bridge;
  the service callback only allocates/protects/release descriptors and wakes
  the Go session, which then reads a fresh snapshot.
- iOS owns NetworkExtension lifetime, tunnel settings, and the app/provider
  command handoff. The Go/Fyne process calls a C bridge in the containing app.
  The app stores raw configuration in an encrypted one-shot Keychain mailbox;
  configuration bytes do not enter provider messages. Go owns session state in
  the provider process. Darwin notifications carry only wake hints, followed
  by a current snapshot read.
- Desktop owns authenticated local transport, service installation/start, and
  local diagnostics. It does not choose protocol policy or parse
  configuration.

The platform shells do not create separate protocol implementations or
session managers.

## Configuration and failures

Go accepts an HTTPS source URL or transient inline configuration. HTTP is
rejected, redirects must remain HTTPS, and both downloaded and inline
configuration are limited to 1 MiB. UI code persists only a URL after
configuration succeeds. Returned profiles omit
dedicated server-address fields and protocol payloads. The user-provided
Description is returned unchanged and may itself contain sensitive text.
Public failure messages use typed, input-safe text; raw URLs, credentials, and
configuration are not echoed in failure responses or logs.

Only `Outline`, `Xray`, `TrustTunnel`, and optional `ExcludeIPs` root entries
are accepted. Any other root section or key—including `Cloak`, `WireGuard`, or
legacy `Telemetry`—rejects the whole configuration with `UNSUPPORTED`, even if
supported profiles are also present. Protocol-owned data nested within a
supported profile remains open to the protocol parser. TrustTunnel profiles
must keep endpoint certificate verification enabled; `skip_verification =
true` or a non-boolean value is rejected as malformed configuration.

Connection selection and health probes contact Google (`/generate_204`),
Cloudflare (`/cdn-cgi/trace`), and `about.google` over the current tunnel route.
Android's VPN service advertises Cloudflare DNS at `1.1.1.1` and
`2606:4700:4700::1111`; iOS tunnel settings advertise `1.1.1.1` and `8.8.8.8`.
The resolver advertised by a platform does not by itself establish the final
DNS path for every protocol. `ExcludeIPs` is an intentional bypass: listed
destinations are routed outside the VPN/proxy, with platform-specific routing
details.

The TrustTunnel native bridge is packaged for physical Android arm64 and iOS
devices. Android x86_64 and iOS Simulator builds intentionally use unsupported
stubs and return a TrustTunnel-specific runtime failure. Android's advertised
Cloudflare DNS list is fixed by the VPN service and does not change with
TrustTunnel's `dns_upstreams` setting.

## Adding a protocol

Add a Go parser section and profile summary, implement the shared runtime
interface, register it in the Go composition root, and cover probing, cleanup,
failure, and cancellation. Update the supported-protocol list and sanitized
examples. Add UI or per-platform protocol controls only for a concrete product
requirement.
