package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.CloakClientConfig
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.OutlineConfig
import com.dobby.feature.main.domain.clearCloakConfig
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

internal class CloakTomlApplier(
    private val cloakRepo: DobbyConfigsRepositoryCloak,
    private val logger: Logger,
) {
    private companion object {
        const val DEFAULT_HTTPS_PORT = 443
        const val DEFAULT_CLOAK_PROXY_METHOD = "shadowsocks"
        const val DEFAULT_CLOAK_NUM_CONN = 8
        const val DEFAULT_CLOAK_STREAM_TIMEOUT = 300
        const val CLOAK_TRANSPORT_CDN = "CDN"
    }

    fun apply(outline: OutlineConfig, cloakEnabled: Boolean) {
        if (!cloakEnabled) {
            cloakRepo.clearCloakConfig()
            return
        }

        logger.log("Cloak enabled inside [Outline], building Cloak config")

        val transport = outline.Transport?.trim().orEmpty().ifEmpty { CLOAK_TRANSPORT_CDN }
        val encryptionMethod = outline.EncryptionMethod?.trim().orEmpty()
        val uid = outline.UID?.trim().orEmpty()
        val publicKey = outline.PublicKey?.trim().orEmpty()

        val hostFromServer = outline.Server?.trim().orEmpty()
        val remoteHost = outline.RemoteHost?.trim().orEmpty().ifEmpty { hostFromServer }
        val remotePort = outline.RemotePort?.trim().orEmpty().ifEmpty {
            outline.Port?.toString() ?: DEFAULT_HTTPS_PORT.toString()
        }
        val serverName = outline.ServerName?.trim().orEmpty().ifEmpty { hostFromServer }
        val cdnOriginHost = outline.CDNOriginHost?.trim().orEmpty().ifEmpty { hostFromServer }

        if (
            transport.isEmpty() ||
            encryptionMethod.isEmpty() ||
            uid.isEmpty() ||
            publicKey.isEmpty() ||
            remoteHost.isEmpty() ||
            remotePort.isEmpty()
        ) {
            logger.log("Invalid [Cloak] fields: EncryptionMethod/UID/PublicKey/RemoteHost/RemotePort are required. Disabling Cloak.")
            cloakRepo.clearCloakConfig()
            return
        }

        val cloakConfig = CloakClientConfig(
            Transport = transport,
            ProxyMethod = outline.ProxyMethod?.trim().orEmpty().ifEmpty { DEFAULT_CLOAK_PROXY_METHOD },
            EncryptionMethod = encryptionMethod,
            UID = uid,
            PublicKey = publicKey,
            ServerName = serverName,
            NumConn = outline.NumConn ?: DEFAULT_CLOAK_NUM_CONN,
            BrowserSig = outline.BrowserSig?.trim()?.takeIf { it.isNotEmpty() },
            StreamTimeout = outline.StreamTimeout ?: DEFAULT_CLOAK_STREAM_TIMEOUT,
            RemoteHost = remoteHost,
            RemotePort = remotePort,
            CDNWsUrlPath = outline.CDNWsUrlPath?.trim()?.takeIf { it.isNotEmpty() },
            CDNOriginHost = cdnOriginHost
        )

        cloakRepo.setIsCloakEnabled(true)
        val cloakJson = buildCloakJson(cloakConfig)
        cloakRepo.setCloakConfig(cloakJson)

        val cloakForLog = cloakConfig.copy(
            UID = maskStr(cloakConfig.UID),
            RemoteHost = maskStr(cloakConfig.RemoteHost),
            ServerName = maskStr(cloakConfig.ServerName),
            CDNOriginHost = maskStr(cloakConfig.CDNOriginHost),
            CDNWsUrlPath = cloakConfig.CDNWsUrlPath?.let { maskStr(it) }
        )
        logger.log("Cloak config saved successfully (config=${buildCloakJson(cloakForLog)})")
    }

    private fun buildCloakJson(config: CloakClientConfig): String {
        val json = Json { prettyPrint = true }
        val obj = buildJsonObject {
            put("Transport", config.Transport)
            put("ProxyMethod", config.ProxyMethod)
            put("EncryptionMethod", config.EncryptionMethod)
            put("UID", config.UID)
            put("PublicKey", config.PublicKey)
            put("ServerName", config.ServerName)
            put("NumConn", config.NumConn)
            config.BrowserSig?.let { put("BrowserSig", it) }
            config.StreamTimeout?.let { put("StreamTimeout", it) }
            put("RemoteHost", config.RemoteHost)
            put("RemotePort", config.RemotePort)
            config.CDNWsUrlPath?.let { put("CDNWsUrlPath", it) }
            put("CDNOriginHost", config.CDNOriginHost)
        }
        return json.encodeToString(obj)
    }
}
