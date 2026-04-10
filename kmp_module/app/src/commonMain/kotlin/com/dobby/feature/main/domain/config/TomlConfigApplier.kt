package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
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
    private val mainRepo: DobbyConfigsRepository,
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

            val xrayResult = xrayApplier.apply(xray)
            logger.log("Finish parseToml() -> Xray applied: $xrayResult")
        }

        // 2. Check for Outline Config
        val outline = root.Outline
        if (outline != null) {
            logger.log("Outline config detected")
            // Ensure Xray is disabled
            disableXray()

            val outlineResult = outlineApplier.apply(outline) ?: run {
                disableOutlineAndCloak()
                logger.log("Finish parseToml()")
                return false
            }
            val (cloakEnabled, _) = outlineResult
            cloakApplier.apply(outline, cloakEnabled)
        }

        val exclude = root.ExcludeIPs

        if (exclude?.IPs != null && exclude.IPs.isNotEmpty()) {
            val cidrsString = exclude.IPs.joinToString(" ")
            logger.log("Applying ExcludeIPs: $cidrsString")
            mainRepo.setGeoRoutingConf(cidrsString)
        } else {
            logger.log("ExcludeIPs not found or empty → clearing routing")
        }

        logger.log("Finish parseToml()")
        return true
    }

    private fun disableOutlineAndCloak() {
        outlineRepo.clearOutlineConfig()
        cloakRepo.clearCloakConfig()
    }

    private fun disableXray() {
        xrayRepo.clearXrayConfig()
    }
}
