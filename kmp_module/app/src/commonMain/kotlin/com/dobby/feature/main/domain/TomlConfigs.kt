package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable
import net.peanuuutz.tomlkt.TomlElement

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

    // Cloak (configured inside an Outline profile)
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
    val CDNOriginHost: String? = null
)

@Serializable
data class ExcludeIPsConfig(
    val IPs: List<String>
)

@Serializable
data class XrayClientConfig(
    val Description: String? = null,
    val version: TomlElement? = null,
    val log: TomlElement? = null,
    val api: TomlElement? = null,
    val dns: TomlElement? = null,
    val routing: TomlElement? = null,
    val policy: TomlElement? = null,
    val inbounds: TomlElement? = null,
    val outbounds: TomlElement? = null,
    val transport: TomlElement? = null,
    val stats: TomlElement? = null,
    val reverse: TomlElement? = null,
    val fakedns: TomlElement? = null,
    val metrics: TomlElement? = null,
    val observatory: TomlElement? = null,
    val burstObservatory: TomlElement? = null,
)


@Serializable
data class TelemetryConfig(
    val Endpoint: String,
    val ApiToken: String,
)

@Serializable
data class ConnectionProfile(
    val protocol: VpnInterface,
    val description: String? = null,
    val sourceIndex: Int,
    val payload: String,
)

@Serializable
data class TomlConfigs(
    val Description: String? = null,
    val Telemetry: TelemetryConfig? = null,
    val Outline: List<OutlineConfig> = emptyList(),
    val Xray: List<XrayClientConfig> = emptyList(),
    val ExcludeIPs: ExcludeIPsConfig? = null
)
