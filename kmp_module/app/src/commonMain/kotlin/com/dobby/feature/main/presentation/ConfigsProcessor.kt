package com.dobby.feature.main.presentation

import com.dobby.feature.main.domain.AmneziaWGConfig
import com.dobby.feature.main.domain.CloakClientConfig
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.XrayClientConfig
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import net.peanuuutz.tomlkt.Toml

/**
 * Utility class to process configs, save it to the [DobbyConfigsRepository]
 * and extract in the required format
 */
class ConfigsProcessor(
    private val configsRepository: DobbyConfigsRepository,
) {
    /**
     * Build attributes string without passwords and with only obfuscation parameters.
     */
    fun buildConfigAttributesJson(): String =
        when (configsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> buildOutlineAttributesJson()

            VpnInterface.AMNEZIA_WG -> buildAmneziaWGAttributesJson()

            VpnInterface.XRAY -> buildXrayAttributesJson()

            VpnInterface.TRUST_TUNNEL -> buildTrustTunnelAttributesJson()

            VpnInterface.NONE -> buildNoneAttributesJson()
        }

    private fun buildNoneAttributesJson(): String =
        Json.encodeToString(
            mapOf(
                "interface" to "None",
            )
        )

    private fun buildTrustTunnelAttributesJson(): String =
        Json.encodeToString(
            mapOf(
                "interface" to "TrustTunnel",
            )
        )

    private fun buildXrayAttributesJson(): String {
        val jsonConfig = configsRepository.getXrayConfig()
        val xrayConfig = if (jsonConfig.isBlank()) {
            null
        } else {
            try {
                Json.decodeFromString<XrayClientConfig>(jsonConfig)
            } catch (_: Exception) {
                null
            }
        }

        return Json.encodeToString(
            mapOf(
                "interface" to "Xray",
                "version" to (xrayConfig?.version?.toString() ?: "null"),
                "log" to (xrayConfig?.log?.toString() ?: "null"),
                "api" to (xrayConfig?.api?.toString() ?: "null"),
                "dns" to (xrayConfig?.dns?.toString() ?: "null"),
                "routing" to (xrayConfig?.routing?.toString() ?: "null"),
                "policy" to (xrayConfig?.policy?.toString() ?: "null"),
                "inbounds" to (xrayConfig?.inbounds?.toString() ?: "null"),
                "outbounds" to (xrayConfig?.outbounds?.toString() ?: "null"),
                "transport" to (xrayConfig?.transport?.toString() ?: "null"),
                "stats" to (xrayConfig?.stats?.toString() ?: "null"),
                "reverse" to (xrayConfig?.reverse?.toString() ?: "null"),
                "fakedns" to (xrayConfig?.fakedns?.toString() ?: "null"),
                "metrics" to (xrayConfig?.metrics?.toString() ?: "null"),
                "observatory" to (xrayConfig?.observatory?.toString() ?: "null"),
                "burstObservatory" to (xrayConfig?.burstObservatory?.toString() ?: "null"),
            )
        )
    }

    private fun buildAmneziaWGAttributesJson(): String {
        val tomlConfigString = configsRepository.getAwgTomlConfig()
        val tomlConfig = if (tomlConfigString.isBlank()) {
            null
        } else {
            try {
                Toml.decodeFromString<AmneziaWGConfig>(tomlConfigString)
            } catch (_: Exception) {
                null
            }
        }

        return Json.encodeToString(
            mapOf(
                "interface" to "AmneziaWG",
                "Jc" to (tomlConfig?.Interface?.Jc?.toString() ?: "null"),
                "Jmin" to (tomlConfig?.Interface?.Jmin?.toString() ?: "null"),
                "Jmax" to (tomlConfig?.Interface?.Jmax?.toString() ?: "null"),
                "S1" to (tomlConfig?.Interface?.S1?.toString() ?: "null"),
                "S2" to (tomlConfig?.Interface?.S2?.toString() ?: "null"),
                "S3" to (tomlConfig?.Interface?.S3?.toString() ?: "null"),
                "S4" to (tomlConfig?.Interface?.S4?.toString() ?: "null"),
                "H1" to (tomlConfig?.Interface?.H1 ?: "null"),
                "H2" to (tomlConfig?.Interface?.H2 ?: "null"),
                "H3" to (tomlConfig?.Interface?.H3 ?: "null"),
                "H4" to (tomlConfig?.Interface?.H4 ?: "null"),
                "I1" to (tomlConfig?.Interface?.I1 ?: "null"),
                "I2" to (tomlConfig?.Interface?.I2 ?: "null"),
                "I3" to (tomlConfig?.Interface?.I3 ?: "null"),
                "I4" to (tomlConfig?.Interface?.I4 ?: "null"),
                "I5" to (tomlConfig?.Interface?.I5 ?: "null"),
            )
        )
    }

    private fun buildOutlineAttributesJson(): String {
        val jsonConfig = configsRepository.getCloakConfig()
        val cloakConfig = if (!configsRepository.getIsCloakEnabled() || jsonConfig.isBlank()) {
            null
        } else {
            try {
                Toml.decodeFromString<CloakClientConfig>(jsonConfig)
            } catch (_: Exception) {
                null
            }
        }

        return Json.encodeToString(
            mapOf(
                "interface" to "Outline",
                "WebSocket" to configsRepository.getIsWebsocketEnabled().toString(),
                "DisguisePrefix" to configsRepository.getPrefixOutline(),
                "TcpPathOutline" to configsRepository.getTcpPathOutline(),
                "UdpPathOutline" to configsRepository.getUdpPathOutline(),
                "Cloak" to configsRepository.getIsCloakEnabled().toString(),
                "Transport" to (cloakConfig?.Transport ?: "null"),
                "ProxyMethod" to (cloakConfig?.ProxyMethod ?: "null"),
                "EncryptionMethod" to (cloakConfig?.EncryptionMethod ?: "null"),
                "UID" to (cloakConfig?.UID ?: "null"),
                "ServerName" to (cloakConfig?.ServerName ?: "null"),
                "NumConn" to (cloakConfig?.NumConn?.toString() ?: "null"),
                "BrowserSig" to (cloakConfig?.BrowserSig ?: "null"),
                "StreamTimeout" to (cloakConfig?.StreamTimeout?.toString() ?: "null"),
                "RemoteHost" to (cloakConfig?.RemoteHost ?: "null"),
                "RemotePort" to (cloakConfig?.RemotePort ?: "null"),
                "CDNWsUrlPath" to (cloakConfig?.CDNWsUrlPath ?: "null"),
                "CDNOriginHost" to (cloakConfig?.CDNOriginHost ?: "null")
            )
        )
    }
}
