package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable

@Serializable
data class ShadowsocksConfig(
    val server: String,
    val port: Int,
    val method: String,
    val password: String,
    val outline: Boolean? = null
)

@Serializable
data class ShadowsocksBlock(
    val local: ShadowsocksConfig? = null,
    val direct: ShadowsocksConfig? = null
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
    val version: String? = null,
    val protocol: String? = null,
    val shadowsocks: ShadowsocksBlock? = null,
    val cloak: CloakConfig? = null
)