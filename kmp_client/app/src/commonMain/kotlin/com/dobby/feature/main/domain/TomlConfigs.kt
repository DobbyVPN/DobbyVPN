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
    val transport: String,
    val proxyMethod: String,
    val encryptionMethod: String,
    val uid: String,
    val publicKey: String,
    val serverName: String,
    val numConn: Int,
    val browserSig: String? = null,
    val streamTimeout: Int? = null,
    val remoteHost: String,
    val remotePort: Int,
    val cdnWsUrlPath: String? = null,
    val cdnOriginHost: String? = null
)

@Serializable
data class TomlConfigs(
    val version: String? = null,
    val protocol: String? = null,
    val shadowsocks: ShadowsocksBlock? = null,
    val cloak: CloakConfig? = null
)