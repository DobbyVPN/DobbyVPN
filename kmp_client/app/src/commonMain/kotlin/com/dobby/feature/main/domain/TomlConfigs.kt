package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable

@Serializable
data class ShadowsocksConfig(
    val Server: String,
    val Port: Int,
    val Method: String,
    val Password: String,
    val Outline: Boolean? = null,
    val Prefix: String? = null,       // Transport wrapper, e.g. "ws:tcp_path=/path"
    val DataPrefix: String? = null    // Shadowsocks data prefix, e.g. "PUT /path HTTP/1.1\r\n"
)

@Serializable
data class ShadowsocksBlock(
    val Local: ShadowsocksConfig? = null,
    val Direct: ShadowsocksConfig? = null
)

@Serializable
data class CloakConfig(
    val Transport: String,
    val ProxyMethod: String,
    val EncryptionMethod: String,
    var UID: String,
    val PublicKey: String,
    var ServerName: String,
    val NumConn: Int,
    val BrowserSig: String? = null,
    val StreamTimeout: Int? = null,
    var RemoteHost: String,
    val RemotePort: String,
    var CDNWsUrlPath: String? = null,
    var CDNOriginHost: String? = null
)

@Serializable
data class TomlConfigs(
    val Version: String? = null,
    val Protocol: String? = null,
    val Shadowsocks: ShadowsocksBlock? = null,
    val Cloak: CloakConfig? = null
)