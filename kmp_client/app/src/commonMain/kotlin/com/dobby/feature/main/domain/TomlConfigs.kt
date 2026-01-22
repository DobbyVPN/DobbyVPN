package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable

@Serializable
data class OutlineConfig(
    val Description: String? = null,
    val Server: String? = null,
    val Port: Int? = null,
    val Method: String? = null,
    val Password: String? = null,
    val WebSocket: Boolean? = null,
    val DisguisePrefix: String? = null,
    val WebSocketPath: String? = null,

    // Cloak (configured inside [Outline])
    val Cloak: Boolean? = null,
    val ProxyMethod: String? = null,
    val Transport: String? = null,
    val EncryptionMethod: String? = null,
    val UID: String? = null,
    val PublicKey: String? = null,
    val ServerName: String? = null,
    val RemoteHost: String? = null,
    val RemotePort: String? = null,
    val CDNWsUrlPath: String? = null,
    val CDNOriginHost: String? = null,
    val NumConn: Int? = null,
    val BrowserSig: String? = null,
    val StreamTimeout: Int? = null
)

@Serializable
data class CloakClientConfig(
    val Transport: String,
    val ProxyMethod: String? = null,
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
data class XrayClientConfig(
    val tag: String?,
    val protocol: String?,
    val address: String?,
    val port: Int?,
    val id: String?,
    val flow: String?,
    val encryption: String?,
    val network: String?,
    val security: String?,
    val fingerprint: String?,
    val serverName: String?,
    val publicKey: String?,
    val shortId: String?,
    val spiderX: String?,
)


@Serializable
data class TomlConfigs(
    // Optional top-level label (some configs put it outside [Outline]); ignored by the app.
    val Description: String? = null,
    val Outline: OutlineConfig? = null,
    val Xray: XrayClientConfig? = null,
)
