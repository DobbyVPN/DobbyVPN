# doBBYVPN - do Better By VPN

Yet another VPN client. Currently wraps around OutlineSDK & cloak.
XRay & AWG are in progress.

Consume 'subscription' / 'dynamic keys' - YAML via HTTPS in one of the following formats:

**Clean ShadowSocks** (best performance)
```toml
[Outline] # Implementation library
Description = "My fast SS" # description - whatever you like
Server = "1.1.1.1" # IP or DNS name for the server
Port = 443 # ShadowSocks port
Password = "Qwerty123" # user's 'secret' from the Outline's config - NOT the part in 'ss://' config
DisguisePrefix = "POST " # one - for TCP & UDP for now; for options - see ref. # 1 below
```

**ShadowSocks via WebSocket** (caddy -> outline-ss-server) 
```toml
[Outline] # Implementation library
Description = "My beautiful SS in WS" # description - whatever you like
WebSocket = true # flag to enable WebSocket
Server = "www.myserver.com" # DNS name of the server
Password = "Qwerty123" # user's 'secret' from the Outline's config
WebSocketPath = "/WS_Ooth5OoCoo7reDah5oich1gai0che2ugh8pho" # listeners.path (one for both TCP & UDP for now) 
DisguisePrefix = "POST " # for options see ref. # 1 below
```

**ShadowSocks over cloak** (caddy -> cloak -> outline-ss-server)
```toml
[Outline] # Implementation library
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
```

Ideas, bugs fixes, features - are welcome as well prepared Pull Requests and nicely expressed Issues accordingly.

Windows and MacOS apps require manual intervention to be installed for now - notarization is a work in progress.

## References:
* 1. [Connection Prefix Disguises](https://developers.google.com/outline/docs/guides/service-providers/prefixing)
* 2. [Cloak](https://github.com/cbeuw/Cloak)
* 3. [quick-cloak-server]([url](https://github.com/DobbyVPN/quick-cloak-server))
