package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.CloakClientConfig
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.TomlConfigs
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

/**
 * Applies TOML connection config to [DobbyConfigsRepository].
 *
 * Important design rule: this class is about configuration only (no VPN start/stop).
 * It should be safe to call multiple times; it always overwrites stored values to avoid stale state.
 */
class TomlConfigApplier(
    private val configsRepository: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private companion object {
        const val DEFAULT_METHOD = "chacha20-ietf-poly1305"
        const val DEFAULT_CLOAK_LOCAL_PORT = 1984
        const val DEFAULT_HTTPS_PORT = 443
        const val DEFAULT_CLOAK_PROXY_METHOD = "shadowsocks"
        const val DEFAULT_CLOAK_NUM_CONN = 8
        const val DEFAULT_CLOAK_BROWSER_SIG = "chrome"
        const val DEFAULT_CLOAK_STREAM_TIMEOUT = 300
    }

    fun apply(connectionConfig: String) {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return
        }

        val root = Toml.decodeFromString<TomlConfigs>(connectionConfig)
        val outline = root.Outline

        if (outline == null) {
            logger.log("Outline config not detected, turning off")
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")
            return
        }

        logger.log("Detected [Outline] config, applying Outline parameters")
        configsRepository.setIsOutlineEnabled(true)

        val method = outline.Method?.trim().orEmpty().ifEmpty { DEFAULT_METHOD }
        val password = outline.Password?.trim().orEmpty()
        val cloakEnabled = outline.Cloak == true
        val websocketEnabled = outline.WebSocket == true

        if (password.isEmpty()) {
            logger.log("Invalid [Outline]: Password is required. Disabling Outline/Cloak.")
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")
            return
        }

        configsRepository.setMethodPasswordOutline("$method:$password")

        // Decide where Outline connects (direct or via local Cloak).
        if (cloakEnabled) {
            configsRepository.setCloakLocalPort(DEFAULT_CLOAK_LOCAL_PORT)
            configsRepository.setServerPortOutline("127.0.0.1:$DEFAULT_CLOAK_LOCAL_PORT")
            logger.log("Cloak enabled: Outline will connect to local endpoint 127.0.0.1:$DEFAULT_CLOAK_LOCAL_PORT (ignoring Outline.Server/Port)")
        } else {
            val server = outline.Server?.trim().orEmpty()
            val port = outline.Port ?: if (websocketEnabled) DEFAULT_HTTPS_PORT else null
            if (server.isEmpty()) {
                logger.log("Invalid [Outline]: Server is required. Disabling Outline.")
                disableOutlineAndCloak()
                logger.log("Finish parseToml()")
                return
            }
            if (port == null) {
                logger.log("Invalid [Outline]: Port is required (unless WebSocket=true, then default is 443). Disabling Outline.")
                disableOutlineAndCloak()
                logger.log("Finish parseToml()")
                return
            }
            configsRepository.setServerPortOutline("${server}:${port}")
            configsRepository.setIsCloakEnabled(false)
            configsRepository.setCloakConfig("")
        }

        // Always persist to avoid stale values from previous configs.
        configsRepository.setIsWebsocketEnabled(websocketEnabled)
        configsRepository.setPrefixOutline(outline.Prefix ?: "") // Don't trim! Spaces may be intentional

        val webSocketPath = outline.WebSocketPath?.trim().orEmpty()
        if (websocketEnabled) {
            configsRepository.setTcpPathOutline(webSocketPath)
            configsRepository.setUdpPathOutline(webSocketPath)
        } else {
            configsRepository.setTcpPathOutline("")
            configsRepository.setUdpPathOutline("")
        }

        logger.log("Outline prefix: ${outline.Prefix ?: "(none)"}")
        logger.log(
            "Outline websocket: $websocketEnabled, " +
                "webSocketPath: ${outline.WebSocketPath ?: "(none)"}"
        )
        logger.log("Outline method, password, and server: ${method}:${maskStr(password)}@${maskStr(configsRepository.getServerPortOutline())}")

        if (cloakEnabled) {
            logger.log("Cloak enabled inside [Outline], building Cloak config")

            val transport = outline.Transport?.trim().orEmpty()
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
                logger.log("Invalid [Outline] Cloak fields: Transport/EncryptionMethod/UID/PublicKey/Server/Port are required. Disabling Cloak.")
                configsRepository.setIsCloakEnabled(false)
                configsRepository.setCloakConfig("")
                logger.log("Finish parseToml()")
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
                BrowserSig = outline.BrowserSig?.trim().orEmpty().ifEmpty { DEFAULT_CLOAK_BROWSER_SIG },
                StreamTimeout = outline.StreamTimeout ?: DEFAULT_CLOAK_STREAM_TIMEOUT,
                RemoteHost = remoteHost,
                RemotePort = remotePort,
                CDNWsUrlPath = outline.CDNWsUrlPath?.trim()?.takeIf { it.isNotEmpty() },
                CDNOriginHost = cdnOriginHost
            )

            configsRepository.setIsCloakEnabled(true)
            val cloakJson = buildCloakJson(cloakConfig, mask = false)
            configsRepository.setCloakConfig(cloakJson)

            val cloakForLog = cloakConfig.copy(
                UID = maskStr(cloakConfig.UID),
                RemoteHost = maskStr(cloakConfig.RemoteHost),
                ServerName = maskStr(cloakConfig.ServerName),
                CDNOriginHost = maskStr(cloakConfig.CDNOriginHost),
                CDNWsUrlPath = cloakConfig.CDNWsUrlPath?.let { maskStr(it) }
            )
            val cloakJsonForLog = buildCloakJson(cloakForLog, mask = true)
            logger.log("Cloak config saved successfully (config=${cloakJsonForLog})")
        }

        logger.log("Finish parseToml()")
    }

    private fun buildCloakJson(config: CloakClientConfig, mask: Boolean): String {
        val json = Json { prettyPrint = true }
        val obj = buildJsonObject {
            put("Transport", config.Transport)
            put("ProxyMethod", config.ProxyMethod)
            put("EncryptionMethod", config.EncryptionMethod)
            put("UID", config.UID)
            put("PublicKey", config.PublicKey)

            // ServerName aliases (compat across Cloak forks/versions)
            put("ServerName", config.ServerName)
            put("SNI", config.ServerName)
            put("server_name", config.ServerName)

            put("NumConn", config.NumConn)
            config.BrowserSig?.let { put("BrowserSig", it) }
            config.StreamTimeout?.let { put("StreamTimeout", it) }

            put("RemoteHost", config.RemoteHost)
            put("RemotePort", config.RemotePort)

            config.CDNWsUrlPath?.let { put("CDNWsUrlPath", it) }
            put("CDNOriginHost", config.CDNOriginHost)
        }
        // We don't log secrets here; caller already masked values if needed.
        return json.encodeToString(obj)
    }

    private fun disableOutlineAndCloak() {
        configsRepository.setIsOutlineEnabled(false)
        configsRepository.setIsCloakEnabled(false)
        configsRepository.setCloakConfig("")
        configsRepository.setPrefixOutline("")
        configsRepository.setIsWebsocketEnabled(false)
        configsRepository.setTcpPathOutline("")
        configsRepository.setUdpPathOutline("")
    }
}
