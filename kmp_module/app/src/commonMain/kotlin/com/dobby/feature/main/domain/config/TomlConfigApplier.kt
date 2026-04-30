package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.*
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

class TomlConfigApplier(
    private val vpnRepo: DobbyConfigsRepositoryVpn,
    private val outlineRepo: DobbyConfigsRepositoryOutline,
    private val cloakRepo: DobbyConfigsRepositoryCloak,
    private val awgRepo: DobbyConfigsRepositoryAwg,
    private val mainRepo: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private val outlineApplier = OutlineTomlApplier(vpnRepo, outlineRepo, cloakRepo, logger)
    private val cloakApplier = CloakTomlApplier(cloakRepo, logger)
    private val awgApplier = AmneziaWGTomlApplier(vpnRepo, awgRepo, logger)

    fun apply(connectionConfig: String): Boolean {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return false
        }

        val root = Toml.decodeFromString<TomlConfigs>(connectionConfig)

        if (root.AmneziaWG != null) {
            return applyAmenziaWG(root.AmneziaWG)
        } else if (root.Outline != null) {
            return applyOutline(root.Outline, root)
        } else {
            return applyNone()
        }
    }

    private fun applyNone(): Boolean {
        logger.log("VPN config not detected, turning off")
        mainRepo.clearVpnConfig()
        logger.log("Finish parseToml()")

        return false
    }

    private fun applyOutline(
        config: OutlineConfig,
        root: TomlConfigs
    ): Boolean {
        logger.log("Outline config detected")

        val outlineResult = outlineApplier.apply(config) ?: run {
            outlineRepo.clearOutlineConfig()
            cloakRepo.clearCloakConfig()
            logger.log("Finish parseToml()")
            return false
        }
        val (cloakEnabled, _) = outlineResult
        cloakApplier.apply(config, cloakEnabled)

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

    private fun applyAmenziaWG(config: AmneziaWGConfig): Boolean {
        logger.log("AmneziaWG config detected")
        awgApplier.apply(config)
        logger.log("Finish parseToml()")

        return true
    }
}
