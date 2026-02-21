package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryAwg
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import com.dobby.feature.main.domain.DobbyConfigsRepositoryVpn
import com.dobby.feature.main.domain.TomlConfigs
import com.dobby.feature.main.domain.clearAwgConfig
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

class TomlConfigApplier(
    private val outlineRepo: DobbyConfigsRepositoryOutline,
    private val cloakRepo: DobbyConfigsRepositoryCloak,
    private val awgRepo: DobbyConfigsRepositoryAwg,
    private val vpnRepo: DobbyConfigsRepositoryVpn,
    private val logger: Logger,
) {
    private val outlineApplier = OutlineTomlApplier(vpnRepo, outlineRepo, cloakRepo, logger)
    private val cloakApplier = CloakTomlApplier(vpnRepo, cloakRepo, logger)
    private val awgApplier = AwgTomlApplier(vpnRepo, awgRepo, logger)

    fun apply(connectionConfig: String): Boolean {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return false
        }

        val root = Toml.decodeFromString<TomlConfigs>(connectionConfig)

        if (root.AWG != null && root.Outline != null) {
            logger.log("Both AmneziaWG and Outline configs detected, turning off")
            disableAmneziaWG()
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")

            return false
        } else if (root.AWG != null) {
            logger.log("AmneziaWG detected")
            awgApplier.apply(root.AWG)
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")

            return true
        } else if (root.Outline != null) {
            val outlineResult = outlineApplier.apply(root.Outline) ?: run {
                disableOutlineAndCloak()
                logger.log("Finish parseToml()")

                return false
            }

            val (cloakEnabled, _) = outlineResult
            cloakApplier.apply(root.Outline, cloakEnabled)
            disableAmneziaWG()
            logger.log("Finish parseToml()")

            return true
        } else {
            logger.log("AmneziaWG or Outline config not detected, turning off")
            disableAmneziaWG()
            disableOutlineAndCloak()
            logger.log("Finish parseToml()")

            return false
        }
    }

    private fun disableOutlineAndCloak() {
        outlineRepo.clearOutlineConfig()
        cloakRepo.clearCloakConfig()
    }

    private fun disableAmneziaWG() {
        awgRepo.clearAwgConfig()
    }
}
