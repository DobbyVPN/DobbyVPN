package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import com.dobby.feature.main.domain.OutlineConfig
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig

internal class OutlineTomlApplier(
    private val outlineRepo: DobbyConfigsRepositoryOutline,
    private val cloakRepo: DobbyConfigsRepositoryCloak,
    private val logger: Logger,
) {
    private companion object {
        const val DEFAULT_METHOD = "chacha20-ietf-poly1305"
        const val DEFAULT_CLOAK_LOCAL_PORT = 1984
        const val DEFAULT_HTTPS_PORT = 443
    }

    fun apply(outline: OutlineConfig): Pair<Boolean, Boolean>? {
        logger.log("Detected [Outline] config, applying Outline parameters")
        outlineRepo.setIsOutlineEnabled(true)

        val method = outline.Method?.trim().orEmpty().ifEmpty { DEFAULT_METHOD }
        val password = outline.Password?.trim().orEmpty()
        val cloakEnabled = outline.Cloak == true
        val websocketEnabled = outline.WebSocket == true

        if (password.isEmpty()) {
            logger.log("Invalid [Outline]: Password is required. Disabling Outline/Cloak.")
            outlineRepo.clearOutlineConfig()
            cloakRepo.clearCloakConfig()
            return null
        }

        outlineRepo.setMethodPasswordOutline("$method:$password")

        // Decide where Outline connects (direct or via local Cloak).
        if (cloakEnabled) {
            cloakRepo.setCloakLocalPort(DEFAULT_CLOAK_LOCAL_PORT)
            outlineRepo.setServerPortOutline("127.0.0.1:$DEFAULT_CLOAK_LOCAL_PORT")
            logger.log("Cloak enabled: Outline will connect to local endpoint 127.0.0.1:$DEFAULT_CLOAK_LOCAL_PORT (ignoring Outline.Server/Port)")
        } else {
            val server = outline.Server?.trim().orEmpty()
            val port = outline.Port ?: if (websocketEnabled) DEFAULT_HTTPS_PORT else null
            if (server.isEmpty()) {
                logger.log("Invalid [Outline]: Server is required. Disabling Outline.")
                outlineRepo.clearOutlineConfig()
                cloakRepo.clearCloakConfig()
                return null
            }
            if (port == null) {
                logger.log("Invalid [Outline]: Port is required (unless WebSocket=true, then default is 443). Disabling Outline.")
                outlineRepo.clearOutlineConfig()
                cloakRepo.clearCloakConfig()
                return null
            }
            outlineRepo.setServerPortOutline("${server}:${port}")
            // Ensure Cloak is cleared when not used.
            cloakRepo.clearCloakConfig()
        }

        // Always persist to avoid stale values from previous configs.
        outlineRepo.setIsWebsocketEnabled(websocketEnabled)
        outlineRepo.setPrefixOutline(outline.Prefix ?: "") // Don't trim! Spaces may be intentional

        val webSocketPath = outline.WebSocketPath?.trim().orEmpty()
        if (websocketEnabled) {
            val base = webSocketPath.trimEnd('/')
            if (base.isBlank()) {
                outlineRepo.setTcpPathOutline("")
                outlineRepo.setUdpPathOutline("")
            } else {
                outlineRepo.setTcpPathOutline("$base/tcp")
                outlineRepo.setUdpPathOutline("$base/udp")
            }
        } else {
            outlineRepo.setTcpPathOutline("")
            outlineRepo.setUdpPathOutline("")
        }

        logger.log("Outline prefix: ${outline.Prefix ?: "(none)"}")
        logger.log("Outline websocket: $websocketEnabled, webSocketPath: ${outline.WebSocketPath ?: "(none)"}")
        logger.log("Outline method, password, and server: ${method}:${maskStr(password)}@${maskStr(outlineRepo.getServerPortOutline())}")

        return cloakEnabled to websocketEnabled
    }
}
