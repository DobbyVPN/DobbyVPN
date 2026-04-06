package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable

@Serializable
data class AmneziaWGConfig(
    val Interface: AmneziaWGInterfaceConfig,
    val Peer: List<AmneziaWGPeerConfig>,
)

@Serializable
data class AmneziaWGInterfaceConfig(
    val PrivateKey: String,
    val PublicKey: String? = null,
    val Address: String,
    val DNS: String? = null,
    val MTU: UInt? = null,
    val Jc: UInt? = null,
    val Jmin: UInt? = null,
    val Jmax: UInt? = null,
    val S1: UInt? = null,
    val S2: UInt? = null,
    val S3: UInt? = null,
    val S4: UInt? = null,
    val H1: UInt? = null,
    val H2: UInt? = null,
    val H3: UInt? = null,
    val H4: UInt? = null,
    val I1: String? = null,
    val I2: String? = null,
    val I3: String? = null,
    val I4: String? = null,
    val I5: String? = null,
)

@Serializable
data class AmneziaWGPeerConfig(
    val PublicKey: String,
    val PresharedKey: String? = null,
    val Endpoint: String,
    val AllowedIPs: String,
    val PersistentKeepalive: Int? = null,
)

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
data class ExcludeIPsConfig(
    val IPs: List<String>
)

@Serializable
data class TomlConfigs(
    val Description: String? = null,
    val Outline: OutlineConfig? = null,
    val AmneziaWG: AmneziaWGConfig? = null,
    val ExcludeIPs: ExcludeIPsConfig? = null
)
