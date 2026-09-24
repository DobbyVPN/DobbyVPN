# doBBYVPN - do Better By VPN

DobbyVPN is a VPN client for Outline, Xray, and TrustTunnel configurations.

## Architecture

Every supported platform uses the same Go backend for configuration loading and
validation, profile selection, VPN session state, protocol runtimes, recovery,
routing, cleanup, and diagnostics.

The visible frontends are native to each supported UI platform:

- SwiftUI on macOS and iOS.
- Kotlin and Jetpack Compose on Android.
- WinUI 3 and C# on Windows.
- Linux has the Go backend and CLI; a Linux GUI is outside the current scope.

The mobile frontends call the shared Go session API through their platform
bridge. The macOS and Windows frontends control the installed Go backend
through a local JSON endpoint: a Unix domain socket on macOS and a fixed named
pipe on Windows. The Go backend runs as a launchd daemon on macOS, a Windows
Service on Windows, and a systemd service on Linux. The operator CLI talks
directly to the backend and is not launched once per frontend action.

The accepted subscription URL is persisted by the Go backend and returned in
its session snapshot. Inline TOML is kept only in the current UI session.
Product logs remain complete and unsanitized; the native frontends read the
local log files and can export their contents.

See the [architecture contract](docs/ARCHITECTURE.md) for component ownership,
local control, URL persistence, and platform boundaries.

The product Go toolchain is pinned in .go-version. Build and test instructions
are in [TESTING.md](TESTING.md), and the complete functional coverage contract
is in [torturer/docs/contract.md](torturer/docs/contract.md). The private owner
Harness runs local checks against disposable guests; its setup and commands
are documented in the private owner workspace.

Windows and macOS package builds and local desktop build commands are described
in [.github/scripts/README.md](.github/scripts/README.md). Release qualifies the
packages it builds. It does not publish them. Publication is a separate manual
step and requires an explicit owner request.

AppStore: https://apps.apple.com/us/app/dobbyvpn-do-better-by-vpn/id6741442515

F-Droid: https://f-droid.org/en/packages/com.dobby.vpn/ (official metadata may
lag releases; availability is not claimed until the index is updated.)

DeepWiki: https://deepwiki.com/DobbyVPN/DobbyVPN

Use TOML configuration inline or fetch it from an HTTPS subscription URL. HTTP
URLs are rejected, redirects must remain HTTPS, and downloaded or inline
configuration is limited to 1 MiB. Supported profile arrays are `Outline`,
`Xray`, and `TrustTunnel`, with optional `[ExcludeIPs]`; any other root section or
key rejects the whole configuration. TrustTunnel certificate verification is
required, so keep `skip_verification = false` (or omit it). This setting is
valid only inside `[TrustTunnel.endpoint]`; a root-level `skip_verification`
key is rejected.

**Connection variants** (automatic probe-based selection and failover)
```toml
[[Outline]] # First variant
Description = "My fast SS"
Server = "1.1.1.1"
Port = 443
Password = "Qwerty123"
DisguisePrefix = "POST "

[[Xray]] # Second variant
Description = "My VLESS Reality"
log = { loglevel = "info" }
outbounds = [
{ tag = "proxy", protocol = "vless", settings = { vnext = [{address = "www.myserver.com", port = 443, users = [{id = "hi8WIXyln+amtgfQeT11zQ==", flow = "xtls-rprx-vision", encryption = "none"}]}]}, streamSettings = {network = "tcp",security = "reality", realitySettings = {show= false, fingerprint = "randomized", serverName = "secretSNI.com", publicKey = "9x3F9q3piIG9yZamqnbl+e6Tr9ZZZrjhfrsqHkG3+Yo=", shortId = "a1b2c3d4", spiderX = "/"}}},
{tag = "direct", protocol = "freedom"}]

[[TrustTunnel]] # Third variant
loglevel = "info"
vpn_mode = "general"
post_quantum_group_enabled = true
exclusions = []
[TrustTunnel.endpoint]
hostname = "domain.com"
addresses = ["ip:port"]
custom_sni = "domain.com"
username = "your_username"
password = "your_password"
client_random = ""
skip_verification = false
upstream_protocol = "http3"
anti_dpi = true
dns_upstreams = []
[TrustTunnel.listener.socks]
address = "127.0.0.1:10808"

# Shared by all variants and kept at the end 
[ExcludeIPs] # Optional
IPs = [
  "200.200.200.200/32"
]
```

DobbyVPN probes configured variants one by one when the VPN starts and
activates the working variant with the lowest average latency, breaking ties
by configuration order. Automatic selection is the GUI behavior. The CLI's
`connect-profile` command and the test harness can select a profile index
through the same session runtime; this operator/test mode skips automatic
selection and does not switch to another profile after a health failure.

After an automatically selected connection becomes unhealthy, DobbyVPN allows
up to three automatic recovery attempts. If another health failure occurs
before five uninterrupted connected minutes, the session fails and the user
must connect again manually. Five uninterrupted connected minutes reset the
recovery allowance. The same `[[Outline]]`, `[[Xray]]`, or `[[TrustTunnel]]`
section format works for one profile or several.

`ExcludeIPs` intentionally bypasses the VPN for the listed destinations. The
traffic still enters the tunnel on some platforms before the runtime routes it
outside the proxy; platform routing details differ. DobbyVPN does not claim a
system-wide kill switch or leak-free recovery during tunnel teardown.

**Clean ShadowSocks** (best performance)
```toml
[[Outline]] # Implementation library
Description = "My fast SS" # description - whatever you like
Server = "1.1.1.1" # IP or DNS name for the server
Port = 443 # ShadowSocks port
Password = "Qwerty123" # user's 'secret' from the Outline's config - NOT the part in 'ss://' config
DisguisePrefix = "POST " # one - for TCP & UDP for now; for options - see ref. # 1 below

[ExcludeIPs] # Optional
IPs = [
  "200.200.200.200/32" # IP adress or subnet that we want to exlude from vpn-routing
]
```

**ShadowSocks via WebSocket** (caddy -> outline-ss-server) 
```toml
[[Outline]] # Implementation library
Description = "My beautiful SS in WS" # description - whatever you like
WebSocket = true # flag to enable WebSocket
Server = "www.myserver.com" # DNS name of the server
Password = "Qwerty123" # user's 'secret' from the Outline's config
WebSocketPath = "/WS_Ooth5OoCoo7reDah5oich1gai0che2ugh8pho" # listeners.path (one for both TCP & UDP for now) 
DisguisePrefix = "POST " # for options see ref. # 1 below

[ExcludeIPs] # Optional
IPs = [
  "200.200.200.200/32" # IP adress or subnet that we want to exlude from vpn-routing
]
```

**VLESS + Reality over xray-core** ([more details](https://xtls.github.io/en/config/outbounds/vless.html))
```toml
[[Xray]] # Implementation library
log = { loglevel = "info" } # Providing DobbyVPN and xray's log level
# Warning: Inbound field will be modified due to custom tunneling settings
outbounds = [
{ tag = "proxy", protocol = "vless", settings = { vnext = [{address = "www.myserver.com", port = 443, users = [{id = "hi8WIXyln+amtgfQeT11zQ==", flow = "xtls-rprx-vision", encryption = "none"}]}]}, streamSettings = {network = "tcp",security = "reality", realitySettings = {show= false, fingerprint = "randomized", serverName = "secretSNI.com", publicKey = "9x3F9q3piIG9yZamqnbl+e6Tr9ZZZrjhfrsqHkG3+Yo=", shortId = "a1b2c3d4", spiderX = "/"}}},
{tag = "direct", protocol = "freedom"}]

[ExcludeIPs] # Optional
IPs = [
	"200.200.200.200/32" # IP adress or subnet that we want to exlude from vpn-routing
]
```

**TrustTunnel** ([more details](https://github.com/TrustTunnel/TrustTunnel))
```toml
[[TrustTunnel]]
loglevel = "info"
vpn_mode = "general"
post_quantum_group_enabled = true
exclusions = []
[TrustTunnel.endpoint]
hostname = "domain.com"
addresses = ["ip:port"]
custom_sni = "domain.com"
username = "your_username"
password = "your_password"
client_random = ""
skip_verification = false
upstream_protocol = "http3"
anti_dpi = true
dns_upstreams = []
[TrustTunnel.listener.socks]
address = "127.0.0.1:10808"
```

Ideas, bugs fixes, features - are welcome as well prepared Pull Requests and nicely expressed Issues accordingly.

See [TESTING.md](TESTING.md) for contributor checks. Pushes and pull requests
run checks. Release starts manually and qualifies its packages with the
functional suite in `torturer/`. A separate manual Publish step uses the
successful Release run's tested artifacts. If Release fails, fix the cause and
start a new Release run; rerunning the old run is unsupported.

Remote telemetry has been removed. `[Telemetry]` configuration blocks are not
supported.

Windows and macOS packages are built and qualified by Release, including
installer lifecycle checks. Distribution signing/notarization remains a
release concern, so locally injected binaries are intended for iteration and
are not distribution artifacts.

## References:
* 1. [Connection Prefix Disguises](https://developers.google.com/outline/docs/guides/service-providers/prefixing)
