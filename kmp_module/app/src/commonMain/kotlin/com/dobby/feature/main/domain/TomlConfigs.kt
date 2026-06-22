package com.dobby.feature.main.domain

import com.dobby.feature.logging.domain.maskStr
import kotlinx.serialization.Serializable
import net.peanuuutz.tomlkt.TomlElement
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

@Serializable
data class AmneziaWGConfig(
    val Interface: AmneziaWGInterfaceConfig,
    val Peer: List<AmneziaWGPeerConfig>,
) {
    fun toAwgQuick(): String {
        val stringBuilder = StringBuilder()
        stringBuilder.append("[Interface]\n")
        stringBuilder.append("PrivateKey = ${Interface.PrivateKey}\n")
        stringBuilder.append("# PublicKey = ${Interface.PublicKey}\n")
        stringBuilder.append("Address = ${Interface.Address}\n")
        Interface.DNS?.let { stringBuilder.append("DNS = $it\n") }
        Interface.MTU?.let { stringBuilder.append("MTU = $it\n") }
        Interface.Jc?.let { stringBuilder.append("Jc = $it\n") }
        Interface.Jmin?.let { stringBuilder.append("Jmin = $it\n") }
        Interface.Jmax?.let { stringBuilder.append("Jmax = $it\n") }
        Interface.S1?.let { stringBuilder.append("S1 = $it\n") }
        Interface.S2?.let { stringBuilder.append("S2 = $it\n") }
        Interface.S3?.let { stringBuilder.append("S3 = $it\n") }
        Interface.S4?.let { stringBuilder.append("S4 = $it\n") }
        Interface.H1?.let { stringBuilder.append("H1 = $it\n") }
        Interface.H2?.let { stringBuilder.append("H2 = $it\n") }
        Interface.H3?.let { stringBuilder.append("H3 = $it\n") }
        Interface.H4?.let { stringBuilder.append("H4 = $it\n") }
        Interface.I1?.let { stringBuilder.append("I1 = $it\n") }
        Interface.I2?.let { stringBuilder.append("I2 = $it\n") }
        Interface.I3?.let { stringBuilder.append("I3 = $it\n") }
        Interface.I4?.let { stringBuilder.append("I4 = $it\n") }
        Interface.I5?.let { stringBuilder.append("I5 = $it\n") }
        stringBuilder.append("\n")

        for (peerConfig in Peer) {
            stringBuilder.append("[Peer]\n")
            stringBuilder.append("PublicKey = ${peerConfig.PublicKey}\n")
            peerConfig.PresharedKey?.let { stringBuilder.append("PresharedKey = $it\n") }
            stringBuilder.append("Endpoint = ${peerConfig.Endpoint}\n")
            stringBuilder.append("AllowedIPs = ${peerConfig.AllowedIPs}\n")
            peerConfig.PersistentKeepalive?.let { stringBuilder.append("PersistentKeepalive = $it\n") }
        }

        return stringBuilder.toString()
    }

    fun toMaskedJson(): String {
        val json = Json { prettyPrint = true }
        val maskedConfig = AmneziaWGConfig(
            Interface = maskInterface(Interface),
            Peer = Peer.map(::maskPeer)
        )

        return json.encodeToString(maskedConfig)
    }

    private fun maskInterface(config: AmneziaWGInterfaceConfig): AmneziaWGInterfaceConfig =
        AmneziaWGInterfaceConfig(
            PrivateKey = maskStr(config.PrivateKey),
            PublicKey = config.PublicKey?.let(::maskStr),
            Address = config.Address,
            DNS = config.DNS,
            MTU = config.MTU,
            Jc = config.Jc,
            Jmin = config.Jmin,
            Jmax = config.Jmax,
            S1 = config.S1,
            S2 = config.S2,
            S3 = config.S3,
            S4 = config.S4,
            H1 = config.H1,
            H2 = config.H2,
            H3 = config.H3,
            H4 = config.H4,
            I1 = config.I1,
            I2 = config.I2,
            I3 = config.I3,
            I4 = config.I4,
            I5 = config.I5,
        )

    private fun maskPeer(config: AmneziaWGPeerConfig): AmneziaWGPeerConfig =
        AmneziaWGPeerConfig(
            PublicKey = maskStr(config.PublicKey),
            PresharedKey = config.PresharedKey?.let(::maskStr),
            Endpoint = config.Endpoint,
            AllowedIPs = config.AllowedIPs,
            PersistentKeepalive = config.PersistentKeepalive,
        )
}

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
    val H1: String? = null,
    val H2: String? = null,
    val H3: String? = null,
    val H4: String? = null,
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
    val Outline: OutlineConfig? = null,
    val AmneziaWG: AmneziaWGConfig? = null,
    val Xray: XrayClientConfig? = null,
    val ExcludeIPs: ExcludeIPsConfig? = null
)

@Serializable
data class MultiTomlConfigs(
    val Description: String? = null,
    val Telemetry: TelemetryConfig? = null,
    val Outline: List<OutlineConfig> = emptyList(),
    val AmneziaWG: List<AmneziaWGConfig> = emptyList(),
    val Xray: List<XrayClientConfig> = emptyList(),
    val ExcludeIPs: ExcludeIPsConfig? = null
)
