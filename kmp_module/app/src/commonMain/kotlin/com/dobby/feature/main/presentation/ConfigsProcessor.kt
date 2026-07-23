package com.dobby.feature.main.presentation

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

            VpnInterface.XRAY -> buildXrayAttributesJson()

            VpnInterface.NONE -> buildNoneAttributesJson()
        }

    private fun buildNoneAttributesJson(): String =
        Json.encodeToString(
            mapOf(
                "interface" to "None",
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
