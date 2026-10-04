# doBBYVPN - do Better By VPN

DobbyVPN is a VPN client for Outline, Xray, and TrustTunnel configurations.

DobbyVPN is source available under the [Business Source License 1.1](LICENSE),
with an additional grant for non-commercial production use. Commercial
production use requires a separate license from Alexander Potemkin. The
Change Date is October 1, 2030, and the Change License is Apache 2.0; see the
license for the conversion terms. [Third-party notices](THIRD_PARTY_NOTICES)
identify components that retain their own licenses.

## Using DobbyVPN

DobbyVPN has native apps for Windows, macOS, Android and iOS. Linux uses the
command-line interface. Desktop installations include a background VPN service
and the `dobby-cli` command.

Enter or paste an HTTPS subscription URL to load its profiles automatically.
Choose **Auto connect** to try profiles in subscription order, or **Connect**
beside a profile to select it. Choosing another profile while connected stops
the old tunnel before starting the new one. Loading a subscription keeps the
current connection running. Failed loads offer **Retry**; **Stop** or
**Disconnect** remains available for the active connection. The backend saves
the accepted URL and restores it when the app reopens. About shows the version,
commit and source link. File and inline configuration remain available in the CLI.

Logs show timestamps, severity labels and colors, source, and readable messages.
Expand **Details** to inspect the original record. Scroll up to hold your reading
position; return to the bottom to follow updates. **Clear** hides earlier entries
across app restarts without deleting diagnostics. Share logs (Save logs on
Windows) exports both retained generations as a gzip file with version and
platform information, including entries hidden by Clear.

An installed native app accepts `dobbyvpn://` to open it and
`dobbyvpn://import?url=https%3A%2F%2Fexample.com%2Fsubscription` to fill and
load a subscription. Imports do not connect or disconnect a tunnel. Linux
continues to use the CLI.

On Linux and other desktop platforms, use `dobby-cli logs` to view diagnostics,
`dobby-cli logs --follow` to follow new entries, or
`dobby-cli logs export diagnostics.gz` to save a compressed snapshot to a new file.
`dobby-cli logs clear` hides earlier entries in the CLI view without removing
retained diagnostics from exports. `DOBBY_CLI_LOG_PATH` selects the CLI log
and its saved view boundary. Each independently owned log keeps a current and previous
generation, rotating after 150 MB while preserving complete records. Product
logs and exports remain unsanitized.

AppStore: https://apps.apple.com/us/app/dobbyvpn-do-better-by-vpn/id6741442515

Android: [Add the DobbyVPN repository to F-Droid](https://fdroid.link/#https://f-repo.dobbyvpn.com/fdroid/repo?fingerprint=F22F23E62C095BEED3A71C4B0D69C7F3A768E52DFE6A83AAF6A2E6AB9E2FEDC4).
With F-Droid installed, open this link, confirm the repository, then install
DobbyVPN. Updates will arrive through F-Droid.
[Repository details and QR code](https://f-repo.dobbyvpn.com/).

From 1.5.3, [GitHub Releases](https://github.com/DobbyVPN/DobbyVPN/releases)
include native Debug builds with a `-debug` filename suffix: Linux DEB,
Windows MSI, macOS PKGs for Intel and Apple silicon, an Android APK, and an
unsigned iOS Xcode archive. These builds retain symbols and disable Go
optimization and inlining for debugging. Prebuilt third-party libraries retain
their upstream build settings.

Desktop Debug installers replace the regular installation. Android Debug uses
a separate app named **DobbyVPN Debug** and a development certificate; a later
Debug APK may require uninstalling the previous Debug app first. The iOS archive
includes dSYMs and requires your own development signing and provisioning
with debugger attachment (`get-task-allow`) enabled for the app and packet-tunnel extension before device installation. Production
updates remain available through the usual channels.

DeepWiki: https://deepwiki.com/DobbyVPN/DobbyVPN

Subscriptions use TOML. The CLI also accepts TOML files and inline configuration.
HTTP URLs are rejected, redirects must remain HTTPS, and downloaded or inline
configuration is limited to 1 MiB. Add each connection variant in an ordered
`[[Outline]]`, `[[Xray]]`, or `[[TrustTunnel]]` section. The optional root
`[ExcludeIPs]` section bypasses the VPN for the destinations in its `IPs` list.
Any other root section or key rejects the whole configuration. TrustTunnel
certificate verification is required, so keep `skip_verification = false`
(or omit it). This setting is valid only inside `[TrustTunnel.endpoint]`; a
profile-level `skip_verification` key is rejected.

**Connection variants** (automatic first-working selection and failover)
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

[ExcludeIPs] # Optional; shared by all variants
IPs = ["200.200.200.200/32"]
```

**Auto connect** tries configured variants in order and keeps the first one whose
tunnel becomes ready. Selecting a profile explicitly uses only that profile,
including during recovery. On desktop,
`dobby-cli configure <file-or-https-url>` accepts a TOML file or HTTPS
subscription URL and returns the ordered backend profile inventory as JSON.
`dobby-cli start --profile <index> --session-id <id> --config-digest <digest>`
starts one accepted profile; `stop --session-id <id> --generation <number>`
ends that generation. Session commands always emit JSON.

After an automatically selected connection becomes unhealthy, DobbyVPN allows
up to three automatic recovery attempts. If another health failure occurs
before five uninterrupted connected minutes, the session fails and the user
must connect again manually. Five uninterrupted connected minutes reset the
recovery allowance. The same ordered `[[Outline]]`, `[[Xray]]`, and
`[[TrustTunnel]]` section format works for one profile or several.

`[ExcludeIPs]` intentionally bypasses the VPN for the destinations in `IPs`. The
traffic still enters the tunnel on some platforms before the runtime routes it
outside the proxy; platform routing details differ. DobbyVPN does not claim a
system-wide kill switch or leak-free recovery during tunnel teardown.

**Clean ShadowSocks** (best performance)
```toml
[[Outline]] # Implementation library
Description = "My fast SS" # whatever you like
Server = "1.1.1.1" # IP or DNS name for the server
Port = 443 # ShadowSocks port
Password = "Qwerty123" # user's 'secret' from the Outline's config - NOT the part in 'ss://' config
DisguisePrefix = "POST " # one - for TCP & UDP for now; for options - see ref. # 1 below

[ExcludeIPs] # Optional
IPs = [
  "200.200.200.200/32" # IP address or subnet to exclude from VPN routing
]
```

**ShadowSocks via WebSocket** (caddy -> outline-ss-server) 
```toml
[[Outline]] # Implementation library
Description = "My beautiful SS in WS" # whatever you like
WebSocket = true # flag to enable WebSocket
Server = "www.myserver.com" # DNS name of the server
Password = "Qwerty123" # user's 'secret' from the Outline's config
WebSocketPath = "/WS_Ooth5OoCoo7reDah5oich1gai0che2ugh8pho" # listeners.path (one for both TCP & UDP for now) 
DisguisePrefix = "POST " # for options see ref. # 1 below

[ExcludeIPs] # Optional
IPs = [
  "200.200.200.200/32" # IP address or subnet to exclude from VPN routing
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
  "200.200.200.200/32" # IP address or subnet to exclude from VPN routing
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

Pushes and pull requests run checks. Release starts manually and qualifies its packages with the
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
