# doBBYVPN - do Better By VPN

Yet another VPN client. Currently wraps around OutlineSDK & cloak.
XRay & AWG are in progress.

DeepWiki: https://deepwiki.com/DobbyVPN/DobbyVPN

Local desktop CLI/VPN checks and CI desktop build commands are documented in
[.github/scripts/README.md](.github/scripts/README.md).

Consume 'subscription' / 'dynamic keys' as TOML via HTTPS or inline:

**Connection variants** (one or more, cyclic fallback)
```toml
[Telemetry] # Optional
Endpoint = "localhost:4318" # Telemetry host shared by all variants
ApiToken = "qwerty-uiop-1234567890" # Ingestion API token

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

[[Outline]] # Third variant
Description = "My sneaky SS in Cloak"
Cloak = true
Server = "www.myserver.com"
Password = "Qwerty123"
BrowserSig = "chrome"
EncryptionMethod = "plain"
UID = "hi8WIXyln+amtgfQeT11zQ=="
PublicKey = "9x3F9q3piIG9yZamqnbl+e6Tr9ZZZrjhfrsqHkG3+Yo="
CDNWsUrlPath = "/JmJWXlmVXByXicD7DGrdMWV1btwHv0ARK0Yjoaig"

# Shared by all variants and kept at the end 
[ExcludeIPs] # Optional
IPs = [
  "200.200.200.200/32"
]
```

DobbyVPN probes protocol variants one by one when the VPN starts. Each variant
must start and pass latency probes through the tunnel; DobbyVPN then activates
the working variant with the lowest average latency. If health check later
reports that the active variant is no longer connected, DobbyVPN repeats the
full probe-and-rank procedure until the user stops the VPN. Use the same
`[[Outline]]`, `[[Xray]]`, or `[[AmneziaWG]]` section format even when the
configuration contains only one variant.

**Clean ShadowSocks** (best performance)
```toml
[Telemetry] # Optional
Endpoint = "localhost:4318" # Telemetry host
ApiToken = "qwerty-uiop-1234567890" # Ingestion API token

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
[Telemetry] # Optional
Endpoint = "localhost:4318" # Telemetry host
ApiToken = "qwerty-uiop-1234567890" # Ingestion API token

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

**ShadowSocks over cloak** (caddy -> cloak -> outline-ss-server)
```toml
[Telemetry] # Optional
Endpoint = "localhost:4318" # Telemetry host
ApiToken = "qwerty-uiop-1234567890" # Ingestion API token

[[Outline]] # Implementation library
Description = "My sneaky SS in Cloak" # description - whatever you like
Cloak = true # enables cloak (what is cloak? see ref # 2 below)
Server = "www.myserver.com"
Password = "Qwerty123" # user's 'secret' from the Outline's config
BrowserSig = "chrome" # or "firefox"
EncryptionMethod = "plain" # plain / aes-256-gcm aka aes-gcm / aes-128-gcm /  chacha20-poly1305; ShadowSocks provides it's own encryption 
# the following three lines are coming from the quick-cloak-server setup script (ref # 3 below); or could be picked up from .env and cloak-server.conf files.  
UID = "hi8WIXyln+amtgfQeT11zQ=="
PublicKey = "9x3F9q3piIG9yZamqnbl+e6Tr9ZZZrjhfrsqHkG3+Yo="
CDNWsUrlPath = "/JmJWXlmVXByXicD7DGrdMWV1btwHv0ARK0Yjoaig"

[ExcludeIPs] # Optional
IPs = [
  "200.200.200.200/32" # IP adress or subnet that we want to exlude from vpn-routing
]
```

For direct Cloak mode, omit `CDNWsUrlPath` or set `Transport = "direct"` explicitly.

**VLESS + Reality over xray-core** ([more details](https://xtls.github.io/en/config/outbounds/vless.html))
```toml
[Telemetry] # Optional
Endpoint = "localhost:4318" # Telemetry host
ApiToken = "qwerty-uiop-1234567890" # Ingestion API token

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

Ideas, bugs fixes, features - are welcome as well prepared Pull Requests and nicely expressed Issues accordingly.

Windows and MacOS apps require manual intervention to be installed for now - notarization is a work in progress.

## References:
* 1. [Connection Prefix Disguises](https://developers.google.com/outline/docs/guides/service-providers/prefixing)
* 2. [Cloak](https://github.com/cbeuw/Cloak)
* 3. [quick-cloak-server]([url](https://github.com/DobbyVPN/quick-cloak-server))
