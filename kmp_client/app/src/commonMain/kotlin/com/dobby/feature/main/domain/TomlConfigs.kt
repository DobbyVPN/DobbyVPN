package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable

@Serializable
data class ShadowsocksConfig(
    val Server: String,
    val Port: Int,
    val Method: String,
    val Password: String,
    val Outline: Boolean? = null
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
    val UID: String,
    val PublicKey: String,
    val ServerName: String,
    val NumConn: Int,
    val BrowserSig: String? = null,
    val StreamTimeout: Int? = null,
    val RemoteHost: String,
    val RemotePort: String,
    val CDNWsUrlPath: String? = null,
    val CDNOriginHost: String? = null
)

@Serializable
data class TomlConfigs(
    val Version: String? = null,
    val Protocol: String? = null,
    val Shadowsocks: ShadowsocksBlock? = null,
    val Cloak: CloakConfig? = null
)