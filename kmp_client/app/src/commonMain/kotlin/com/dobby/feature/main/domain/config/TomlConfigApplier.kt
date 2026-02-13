package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import com.dobby.feature.main.domain.DobbyConfigsRepositoryXray
import com.dobby.feature.main.domain.TomlConfigs
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig
import com.dobby.feature.main.domain.clearXrayConfig
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

class TomlConfigApplier(
    private val outlineRepo: DobbyConfigsRepositoryOutline,
    private val cloakRepo: DobbyConfigsRepositoryCloak,
    private val xrayRepo: DobbyConfigsRepositoryXray,
    private val logger: Logger,
) {
    private val outlineApplier = OutlineTomlApplier(outlineRepo, cloakRepo, logger)
    private val cloakApplier = CloakTomlApplier(cloakRepo, logger)
    private val xrayApplier = XrayTomlApplier(xrayRepo, logger)

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

        // 1. Check for Xray Config
        val xray = root.Xray
        if (xray != null) {
            logger.log("Xray config detected")
            // Ensure other configs are disabled
            disableOutlineAndCloak()

            val result = xrayApplier.apply(xray)
            logger.log("Finish parseToml() -> Xray applied: $result")
            return result
        }

        // 2. Check for Outline Config
        val outline = root.Outline
        if (outline != null) {
            // Ensure Xray is disabled
            disableXray()

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

        logger.log("No valid config (Outline or Xray) detected, turning off all")
        disableOutlineAndCloak()
        disableXray()
        return false
    }

    private fun disableOutlineAndCloak() {
        outlineRepo.clearOutlineConfig()
        cloakRepo.clearCloakConfig()
    }

    private fun disableXray() {
        xrayRepo.clearXrayConfig()
    }
}
