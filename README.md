# doBBYVPN - do Better By VPN

Yet another VPN client. Currently wraps around OutlineSDK & cloak.

Consume 'subscription' / 'dynamic keys' - YAML via HTTPS in one of the following formats:

**Clean ShadowSocks** (best performance)
```yaml
[Outline]
Description = "My SS connect"
Server = "1.1.1.1"
Port = 443
Password = "Qwerty123"
DisguisePrefix = "POST " # see ref. # 1 below
```

**ShadowSocks via WebSocket** (caddy -> outline-ss-server) 
```yaml
[Outline]
Description = "My SS over WS"
WebSocket = true # flag to enable WebSocket
Server = "www.myserver.com"
Password = "Qwerty123"
WebSocketPath = "/WS_Ooth5OoCoo7reDah5oich1gai0che2ugh8pho" # one URL for both TCP and UDP for simplicity for now 
```

**ShadowSocks over cloak** (caddy -> cloak -> outline-ss-server)
```
[Outline]
Description = "My SS over Cloak"
Cloak = true # enables cloak - see ref # 2 below
Server = "www.myserver.com"
Password = "Qwerty123"
BrowserSig = "chrome" # or "firefox"
EncryptionMethod = "plain" # plain / aes-256-gcm aka aes-gcm / aes-128-gcm /  chacha20-poly1305
# the following three lines are coming from the cloak setup script
UID = "hi8WIXyln+amtgfQeT11zQ=="
PublicKey = "9x3F9q3piIG9yZamqnbl+e6Tr9ZZZrjhfrsqHkG3+Yo="
CDNWsUrlPath = "/JmJWXlmVXByXicD7DGrdMWV1btwHv0ARK0Yjoaig"
```

Ideas, bugs fixes, features - are welcome as well prepared Pull Requests and nicely expressed Issues accordingly.

Windows and MacOS apps require manual intervention to be installed for now - notarization is a work in progress.

## References:
* 1 [Connection Prefix Disguises](https://developers.google.com/outline/docs/guides/service-providers/prefixing)
* 2 [Cloak](https://github.com/cbeuw/Cloak)
