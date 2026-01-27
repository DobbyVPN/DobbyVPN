package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import com.dobby.feature.main.domain.TomlConfigs
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

class TomlConfigApplier(
    private val outlineRepo: DobbyConfigsRepositoryOutline,
    private val cloakRepo: DobbyConfigsRepositoryCloak,
    private val logger: Logger,
) {
    private val outlineApplier = OutlineTomlApplier(outlineRepo, cloakRepo, logger)
    private val cloakApplier = CloakTomlApplier(cloakRepo, logger)

    fun apply(connectionConfig: String): Boolean {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return false
        }

        val root = Toml.decodeFromString<TomlConfigs>(connectionConfig)
        val outline = root.Outline

        if (outline == null) {
            logger.log("Outline config not detected, turning off")
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")
            return false
        }

        val outlineResult = outlineApplier.apply(outline) ?: run {
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")
            return false
        }
        val (cloakEnabled, _) = outlineResult
        cloakApplier.apply(outline, cloakEnabled)
        logger.log("Finish parseToml()")
        return true
    }

    private fun disableOutlineAndCloak() {
        outlineRepo.clearOutlineConfig()
        cloakRepo.clearCloakConfig()
    }
}
