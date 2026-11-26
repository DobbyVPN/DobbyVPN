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

### Supported platforms

| Platform | Minimum version | Architectures | Package |
|---|---|---|---|
| Windows | Windows 10 version 1809 (build 17763) | x64 | MSI |
| macOS | macOS 12 | Apple silicon (arm64), Intel (x86_64) | PKG |
| Linux | Debian/Ubuntu-based systems; requires `init-system-helpers` 1.54 or later | amd64 | DEB |
| Android | Android 15 (API 35); installs on Android 8.0 (API 26) and later | arm64-v8a, x86_64 | APK |
| iOS | iOS 15.6 | arm64 | App Store |

Android permits installation on Android 8.0 (API 26) and later. Current testing
uses Android 15 (API 35), and support is limited to Android 15 and later.
Android 8–14 are not currently tested or supported.

Where `dobby-cli` is installed:

- **macOS:** the package links `/usr/local/bin/dobby-cli` to the copy inside
  the app, on both Intel and Apple silicon, so `dobby-cli` works from a new
  terminal. The linked file is
  `/Applications/Dobby VPN.app/Contents/Resources/dobby-cli`.
- **Linux:** the DEB links `/usr/local/bin/dobby-cli` to
  `/opt/dobbyvpn/bin/dobby-cli`.
- **Windows:** the installer does not change `PATH`. Run the command by its
  full path, normally `"C:\Program Files\DobbyVPN\bin\dobby-cli.exe"`
  (use `& "C:\Program Files\DobbyVPN\bin\dobby-cli.exe" snapshot` in
  PowerShell).

After installing a desktop package, `dobby-cli snapshot` connects to the
installed background service. On macOS it uses the packaged socket without
an environment override; on Linux the service socket lives in the system
runtime directory `/run/dobbyvpn/control.sock` and works without a desktop
session. `DOBBYVPN_CONTROL_SOCKET` can still select an alternative Unix
socket when needed.

Linux DEB installation starts the service when system policy permits. Upgrades
preserve disabled and masked services. The first upgrade from the older 1.5.4
package may re-enable a disabled, unmasked service because that package did
not retain enablement history; later upgrades preserve the administrator's
choice. Removing or purging the package preserves subscription data.

On a remotely managed Linux host, configure a systemd drop-in for
`dobbyvpn.service` before enabling VPN auto-connect. Under `[Service]`, set
`Environment="DOBBYVPN_LINUX_MANAGEMENT_PEERS=192.0.2.10"`, replacing the
example with the IP address your administrator connects from. You can list
multiple exact IPv4 and IPv6 addresses separated by spaces. The service keeps
the physical return path for TCP connections that those peers initiate.
Connections initiated by the Linux host continue to follow the VPN policy.


### Connecting for the first time on macOS

Install the universal package and open **Dobby VPN** from Applications. On the first connection, macOS asks you to approve **DobbyVPN Protection** and **DobbyVPN PacketTunnel** in System Settings. Allow Dobby VPN to add its VPN configuration and filter network traffic. The window and `dobby-cli` use the same connection and approvals. Keep System Integrity Protection enabled.

### Uninstalling on macOS

```sh
sudo /usr/local/libexec/dobbyvpn-uninstall
```

macOS may ask for administrator approval to remove each system extension. The script must run as root. It stops and unregisters the
`com.dobby.vpnservice` background service, then removes
`/Library/LaunchDaemons/com.dobby.vpnservice.plist`, the control socket
`/var/run/dobbyvpn/control.sock` (and its directory if empty), the
`com.dobby.pkg` package receipt, `/Applications/Dobby VPN.app` and the script
itself. Logs in `/Library/Logs/DobbyVPN` are not removed.

Enter or paste an HTTPS subscription URL to load its profiles automatically.
Choose **Auto connect** to try profiles in subscription order, or **Connect**
beside a profile to select it. Choosing another profile while connected stops
the old protocol connection before starting the new one, retaining the VPN
interface. If the new connection fails, DobbyVPN
does not reconnect the old profile automatically. Loading a subscription keeps
the current connection running. Failed loads offer **Retry**; **Stop** or
**Disconnect** remains available for the active connection. The backend saves
the accepted URL and restores it when the app reopens. The startup log shows the
version and commit. File and inline configuration remain available in the CLI.

The VPN interface, routes and DNS stay active while DobbyVPN switches protocols.
If no protocol works, the app shows the failure with **Retry** and **Stop**.
Retry uses the saved selection without removing the interface; Stop releases
the app's VPN protection. Existing application connections may need to reconnect
after a transport failure.

VPN DNS uses `9.9.9.9`, `1.1.1.1`, `2620:fe::fe` and
`2606:4700:4700::1111`. Gateway hostnames use the underlying network's DNS.
IPv4 and IPv6 application traffic are supported independently of the gateway's
address family. IPv6 destinations require IPv6 connectivity at the VPN exit. Xray configurations retain their protocol and transport settings,
including Trojan and Hysteria2.

Android supports the system's **Always-on VPN** and **Block connections without
VPN** settings. On iOS, connect once to save a selection, then enable **Connect on
demand** to let the system start it automatically. Stop disables On Demand.

Logs show timestamps and messages, with emphasis for warnings and errors.
Complete exports retain severity and source fields.
Scroll up to hold your reading position; return to the bottom to follow updates.
**Clear** hides earlier entries
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
and its saved view boundary. Diagnostics use two destinations, each with current and previous
generations (at most four retained files in total), rotating after 150 MB while preserving complete records. Product
logs and exports remain unsanitized.

When enabled by your provider, Upload in the log menu sends a complete compressed
snapshot and shows a support reference after the server confirms it. You can
cancel the upload, copy the full reference, or share its text. Linux and other
desktop platforms also support `dobby-cli logs upload`. Manual Share/Save remains
available if an upload fails or exceeds the size limit.

For providers, the [HTTPS log-upload guide](docs/log-upload.md) explains the
optional subscription setting, version compatibility and a stock Caddy example.


AppStore: https://apps.apple.com/us/app/dobbyvpn-do-better-by-vpn/id6741442515

Android: [Add the DobbyVPN repository to F-Droid](https://fdroid.link/#https://f-repo.dobbyvpn.com/fdroid/repo?fingerprint=F22F23E62C095BEED3A71C4B0D69C7F3A768E52DFE6A83AAF6A2E6AB9E2FEDC4).
With F-Droid installed, open this link, confirm the repository, then install
DobbyVPN. Updates will arrive through F-Droid.
[Repository details and QR code](https://f-repo.dobbyvpn.com/).

From 1.5.3, [GitHub Releases](https://github.com/DobbyVPN/DobbyVPN/releases)
include native Debug builds with a `-debug` filename suffix: Linux DEB,
Windows MSI, a universal macOS PKG for Intel and Apple silicon, an Android APK, and an
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
`[ExcludeIPs]` section bypasses the VPN for connections you open to destinations in its `IPs` list.
Listing a peer leaves the policy for incoming connections from that peer unchanged.
An optional `logs_endpoint` root setting enables provider log uploads. Place it before the first table, including when piping the subscription to `dobby-cli configure -`. Other root sections or keys are rejected. TrustTunnel
certificate verification is required, so keep `skip_verification = false`
(or omit it). This setting is valid only inside `[TrustTunnel.endpoint]`; a
profile-level `skip_verification` key is rejected. In `[[Xray]]`, the first
outbound carries all traffic and must not be `freedom`. Only `outbounds`, `dns`,
`policy` and `log` (`loglevel`, `dnsLog`, `maskAddress`) are passed to Xray;
other Xray settings are ignored. A subscription is never rejected because of
the addresses listed in `ExcludeIPs`; malformed TOML or unknown keys in that
section are still format errors. An invalid address or network and an entry with
prefix `/0` (`0.0.0.0/0`, `::/0`, `::ffff:0.0.0.0/96`) are dropped. If the IPv4
entries together cover half of IPv4 or more, all IPv4 entries, single addresses
included, are dropped; the same applies to IPv6 entries when they cover half of
`2000::/3` or more. Unique local and link-local IPv6 entries are kept. A host
name that cannot be resolved at connection time is ignored. Dropped traffic
stays in the VPN, and the service log gets a warning with counts only.

If a subscription is rejected, the error names the profile (its position in the file, counted from 1) and the field. A response that is not a DobbyVPN subscription (HTML, JSON, YAML, a base64 list or a single share link such as `vless://`) is reported as such; it is not converted. Unknown `[[Outline]]` fields, a non-string `DisguisePrefix`, and a `WebSocketPath` without `WebSocket = true` do not reject the subscription; they are written to the service log as warnings.

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
It also accepts a complete TOML subscription as a single argument quoted for
your shell. Root settings can precede the first profile table in the inline subscription.
`dobby-cli configure -` reads the HTTPS subscription URL (or a TOML profile)
from standard input instead, so the URL does not appear in the process list or
shell history: `printf '%s\n' "$SUBSCRIPTION_URL" | dobby-cli configure -`.
Stdin accepts a URL or TOML only, never a file path; to configure from a file
whose name is `-`, write `./-`.
`dobby-cli start --profile <index> --session-id <id> --config-digest <digest>`
starts one accepted profile; `stop --session-id <id> --generation <number>`
ends that generation. Session commands always emit JSON.

`dobby-cli` exits with one of these codes, so scripts can tell the cases apart:

| Code | Meaning |
| ---- | ------- |
| 0 | Success. |
| 2 | Wrong command line or options, or a rejected configuration source. |
| 3 | The Dobby VPN service is unavailable: it is stopped, not installed, or access to it was denied. The message names the check to run, and JSON commands print the failure code `SERVICE_UNAVAILABLE`. |
| 4 | The operation failed, including timeouts and failures reported by the service. |
| 8 | Conflict: the accepted configuration or the session generation changed; take a new `snapshot` and retry. |

The command line is checked before anything is written or connected, so a typo
exits with 2 even when the service is down.

After an automatically selected connection becomes unhealthy, DobbyVPN allows
up to three automatic recovery attempts. If another health failure occurs
before five uninterrupted connected minutes, the connection fails while VPN
protection remains active. Choose Retry to start another selection attempt or
Stop to release it. Five uninterrupted connected minutes reset the
recovery allowance. The same ordered `[[Outline]]`, `[[Xray]]`, and
`[[TrustTunnel]]` section format works for one profile or several.

`[ExcludeIPs]` intentionally bypasses the VPN for the destinations in `IPs`. The
traffic still enters the tunnel on some platforms before the runtime routes it
outside the proxy. On Linux and Windows, base firewall protection can remain
active after the VPN service exits unexpectedly. On restart, the service
reconciles that policy. Use Stop to release the protection. Behavior after
shutdown or reboot depends on the platform. Android's optional system lockdown is controlled in Android
settings.

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

Remote telemetry has been removed. `[Telemetry]` configuration blocks are not
supported.

## macOS package signing

From 1.5.17, the distributed Release and Debug installers use Developer ID signing and Apple notarization. The Debug package includes symbols. For live debugger attachment, use a development-signed build with the debugging entitlement.

## Pull request reviews

Ready (non-draft) pull requests receive an automated review pass once CI checks
complete:

- **`critic: passed`:** No blocking defects were found on the current head
  commit. Pushing a new commit clears this label until the updated commit is
  reviewed.
- **`critic: needs fixes`:** One or more blocking defects were flagged in the
  review comment. Pushing fixes triggers a follow-up review that updates the
  label automatically.

## References:
* 1. [Connection Prefix Disguises](https://developers.google.com/outline/docs/guides/service-providers/prefixing)
