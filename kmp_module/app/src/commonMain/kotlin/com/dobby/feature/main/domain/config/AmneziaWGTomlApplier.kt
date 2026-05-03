package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.AmneziaWGConfig
import com.dobby.feature.main.domain.DobbyConfigsRepositoryAwg
import com.dobby.feature.main.domain.DobbyConfigsRepositoryVpn
import com.dobby.feature.main.domain.VpnInterface
import kotlinx.serialization.encodeToString
import net.peanuuutz.tomlkt.Toml

internal class AmneziaWGTomlApplier(
    val vpnRepo: DobbyConfigsRepositoryVpn,
    private val awgRepo: DobbyConfigsRepositoryAwg,
    private val logger: Logger,
) {
    fun apply(amneziaWGConfig: AmneziaWGConfig) {
        logger.log("Detected [AmneziaWG] config, applying AmneziaWG parameters")

        // TODO: validate amneziaWGConfig

        val tomlConfig = amneziaWGConfig.toAwgQuick()
        val maskedConfig = amneziaWGConfig.toMaskedJson()

        vpnRepo.setVpnInterface(VpnInterface.AMNEZIA_WG)
        awgRepo.setIsAmneziaWGEnabled(true)
        awgRepo.setAwgConfig(tomlConfig)
        awgRepo.setAwgTomlConfig(Toml.encodeToString(amneziaWGConfig))

        logger.log("AmneziaWG config saved successfully (config=$maskedConfig)")
    }
}
