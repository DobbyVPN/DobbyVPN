package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import com.dobby.feature.main.domain.DobbyConfigsRepositoryTrustTunnel
import com.dobby.feature.main.domain.TomlConfigs
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig
import com.dobby.feature.main.domain.clearTrustTunnelConfig
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

class TomlConfigApplier(
    private val outlineRepo: DobbyConfigsRepositoryOutline,
    private val cloakRepo: DobbyConfigsRepositoryCloak,
    private val trustTunnelRepo: DobbyConfigsRepositoryTrustTunnel,
    private val logger: Logger,
) {
    private val outlineApplier = OutlineTomlApplier(outlineRepo, cloakRepo, logger)
    private val cloakApplier = CloakTomlApplier(cloakRepo, logger)
    private val trustTunnelApplier = TrustTunnelTomlApplier(trustTunnelRepo, logger)

    fun apply(connectionConfig: String): Boolean {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return false
        }

        val root = try {
            Toml.decodeFromString<TomlConfigs>(connectionConfig)
        } catch (e: Exception) {
            logger.log("Failed to parse TOML: ${e.message}")
            return false
        }

        val trustTunnel = root.TrustTunnel
        if (trustTunnel != null) {
            logger.log("TrustTunnel config detected")
            disableOutlineAndCloak()

            val result = trustTunnelApplier.apply(trustTunnel)
            logger.log("Finish parseToml() -> TrustTunnel applied: $result")
            return result
        }

        val outline = root.Outline
        if (outline != null) {
            disableTrustTunnel()

            val outlineResult = outlineApplier.apply(outline) ?: run {
                disableOutlineAndCloak()
                logger.log("Finish parseToml() -> Outline failed")
                return false
            }
            val (cloakEnabled, _) = outlineResult
            cloakApplier.apply(outline, cloakEnabled)
            logger.log("Finish parseToml() -> Outline applied")
            return true
        }

        logger.log("No valid config detected, turning off all")
        disableOutlineAndCloak()
        disableTrustTunnel()

        logger.log("Finish parseToml()")
        return true
    }

    private fun disableOutlineAndCloak() {
        outlineRepo.clearOutlineConfig()
        cloakRepo.clearCloakConfig()
    }

    private fun disableTrustTunnel() {
        trustTunnelRepo.clearTrustTunnelConfig()
    }
}
