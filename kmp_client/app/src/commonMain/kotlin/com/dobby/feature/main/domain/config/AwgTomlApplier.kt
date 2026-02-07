package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.DobbyConfigsRepositoryAwg
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig

internal class AwgTomlApplier(
    private val awgRepo: DobbyConfigsRepositoryAwg,
    private val logger: Logger,
) {
    fun apply(config: String) {
        logger.log("Detected [AmneziaWG] config, applying AmneziaWG parameters")
        awgRepo.setIsAmneziaWGEnabled(true)
        awgRepo.setAwgConfig(config)
    }
}
