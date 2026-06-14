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
    private val xrayRepo: DobbyConfigsRepositoryXray,
    private val mainRepo: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private val outlineApplier = OutlineTomlApplier(vpnRepo, outlineRepo, cloakRepo, logger)
    private val cloakApplier = CloakTomlApplier(cloakRepo, logger)
    private val awgApplier = AmneziaWGTomlApplier(vpnRepo, awgRepo, logger)
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

        // 0. Set telemetry server
        mainRepo.setTelemetryEndpoint(root.Telemetry ?: "")

        // 1. Check for Xray Config
        val xray = root.Xray
        if (xray != null) {
            return applyXray(xray)
        }

        // 2. Check for Outline Config
        val outline = root.Outline
        if (outline != null) {
            return applyOutline(outline, root)
        }

        // 3. Check for AWG Config
        val awg = root.AmneziaWG
        if (awg != null) {
            return applyAmenziaWG(awg)
        }

        logger.log("Unsupported config")
        return false
    }

    private fun applyNone(): Boolean {
        logger.log("VPN config not detected, turning off")
        mainRepo.clearVpnConfig()
        logger.log("Finish parseToml()")

        return false
    }

    private fun applyXray(config: XrayClientConfig): Boolean {
        logger.log("Xray config detected")
        xrayApplier.apply(config)
        logger.log("Finish parseToml()")

        return true
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
            val sample = exclude.IPs.take(5).joinToString(" ")
            logger.log("Applying ExcludeIPs: count=${exclude.IPs.size} size=${cidrsString.length} sample=$sample")
            mainRepo.setGeoRoutingConf(cidrsString)
        } else {
            logger.log("ExcludeIPs not found or empty → clearing routing")
            mainRepo.setGeoRoutingConf("")
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
