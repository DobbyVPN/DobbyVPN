package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable

@Serializable
data class OutlineConfig(
    // When Cloak=true, Server/Port may be omitted and will be ignored (Outline will connect to 127.0.0.1:LocalPort).
    val Server: String? = null,
    val Port: Int? = null,
    val Method: String,
    val Password: String,
    // If true - use WebSocket transport; if absent/false - plain Outline (direct Shadowsocks).
    val Websocket: Boolean? = null,
    val Prefix: String? = null,
    val TcpPath: String? = null,
    val UdpPath: String? = null,

    // Cloak (configured inside [Outline])
    val Cloak: Boolean? = null,
    val LocalPort: Int = 1984,
    val Transport: String? = null,
    val EncryptionMethod: String? = null,
    val UID: String? = null,
    val PublicKey: String? = null,
    val ServerName: String? = null,
    val RemoteHost: String? = null,
    val RemotePort: String? = null,
    val CDNWsUrlPath: String? = null,
    val CDNOriginHost: String? = null,
    // Defaults if not provided in TOML (when Cloak enabled)
    val NumConn: Int = 8,
    val BrowserSig: String = "chrome",
    val StreamTimeout: Int = 300
)

@Serializable
data class CloakClientConfig(
    val Transport: String,
    // Not exposed in TOML; always "shadowsocks"
    val ProxyMethod: String = "shadowsocks",
    val EncryptionMethod: String,
    val UID: String,
    val PublicKey: String,
    val ServerName: String,
    val NumConn: Int,
    val BrowserSig: String? = null,
    val StreamTimeout: Int? = null,
    val RemoteHost: String,
    val RemotePort: String,
    var CDNWsUrlPath: String? = null,
    val CDNOriginHost: String
)

@Serializable
data class TomlConfigs(
    val Version: String? = null,
    val Protocol: String? = null,
    val Outline: OutlineConfig? = null,
)