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

        val method = outline.Method.trim()
        val password = outline.Password.trim()
        val cloakEnabled = outline.Cloak == true

        if (method.isEmpty() || password.isEmpty()) {
            logger.log("Invalid [Outline]: Method/Password are required. Disabling Outline/Cloak.")
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")
            return
        }

        configsRepository.setMethodPasswordOutline("$method:$password")

        // Decide where Outline connects (direct or via local Cloak).
        if (cloakEnabled) {
            val localPort = if (outline.LocalPort in 1..65535) outline.LocalPort else 1984
            if (outline.LocalPort !in 1..65535) {
                logger.log("Invalid Outline.LocalPort=${outline.LocalPort}; using default 1984")
            }
            configsRepository.setCloakLocalPort(localPort)
            configsRepository.setServerPortOutline("127.0.0.1:$localPort")
            logger.log("Cloak enabled: Outline will connect to local endpoint 127.0.0.1:$localPort (ignoring Outline.Server/Port)")
        } else {
            val server = outline.Server?.trim().orEmpty()
            val port = outline.Port
            if (port == null) {
                logger.log("Invalid [Outline]: Port is required Disabling Outline.")
                disableOutlineAndCloak()
                logger.log("Finish parseToml()")
                return
            }
            if (server.isEmpty()) {
                logger.log("Invalid [Outline]: Server is required. Disabling Outline.")
                disableOutlineAndCloak()
                logger.log("Finish parseToml()")
                return
            }
            configsRepository.setServerPortOutline("${server}:${port}")
            configsRepository.setIsCloakEnabled(false)
            configsRepository.setCloakConfig("")
        }

        // Always persist to avoid stale values from previous configs.
        val websocketEnabled = outline.Websocket == true
        configsRepository.setIsWebsocketEnabled(websocketEnabled)
        configsRepository.setPrefixOutline(outline.Prefix ?: "") // Don't trim! Spaces may be intentional
        configsRepository.setTcpPathOutline(outline.TcpPath?.trim() ?: "")
        configsRepository.setUdpPathOutline(outline.UdpPath?.trim() ?: "")

        logger.log("Outline prefix: ${outline.Prefix ?: "(none)"}")
        logger.log("Outline websocket: $websocketEnabled, tcpPath: ${outline.TcpPath ?: "(none)"}, udpPath: ${outline.UdpPath ?: "(none)"}")
        logger.log("Outline method, password, and server: ${method}:${maskStr(password)}@${maskStr(configsRepository.getServerPortOutline())}")

        if (cloakEnabled) {
            logger.log("Cloak enabled inside [Outline], building Cloak config")

            val transport = outline.Transport?.trim().orEmpty()
            val encryptionMethod = outline.EncryptionMethod?.trim().orEmpty()
            val uid = outline.UID?.trim().orEmpty()
            val publicKey = outline.PublicKey?.trim().orEmpty()
            val remoteHost = outline.RemoteHost?.trim().orEmpty()
            val remotePort = outline.RemotePort?.trim().orEmpty()

            if (
                transport.isEmpty() ||
                encryptionMethod.isEmpty() ||
                uid.isEmpty() ||
                publicKey.isEmpty() ||
                remoteHost.isEmpty() ||
                remotePort.isEmpty()
            ) {
                logger.log("Invalid [Outline] Cloak fields: Transport/EncryptionMethod/UID/PublicKey/RemoteHost/RemotePort are required. Disabling Cloak.")
                configsRepository.setIsCloakEnabled(false)
                configsRepository.setCloakConfig("")
                logger.log("Finish parseToml()")
                return
            }

            val serverName = outline.ServerName?.trim().orEmpty().ifEmpty { remoteHost }
            val cdnOriginHost = outline.CDNOriginHost?.trim().orEmpty().ifEmpty { remoteHost }

            val cloakConfig = CloakClientConfig(
                Transport = transport,
                EncryptionMethod = encryptionMethod,
                UID = uid,
                PublicKey = publicKey,
                ServerName = serverName,
                NumConn = outline.NumConn,
                BrowserSig = outline.BrowserSig,
                StreamTimeout = outline.StreamTimeout,
                RemoteHost = remoteHost,
                RemotePort = remotePort,
                CDNWsUrlPath = outline.CDNWsUrlPath?.trim(),
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

    /**
     * Cloak JSON must match what the Go Cloak RawConfig expects.
     * Historically different builds expect different keys (e.g. ServerName vs SNI vs server_name),
     * so we emit a small compatibility set of aliases.
     *
     * Important: do NOT replace this with plain `Json.encodeToString(CloakClientConfig)` unless you
     * also keep these aliases and enable defaults. Some Cloak builds read SNI/server_name instead
     * of ServerName, and default fields (like ProxyMethod) may be required.
     */
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
