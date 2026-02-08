package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryAwg

internal class AwgTomlApplier(
    private val awgRepo: DobbyConfigsRepositoryAwg,
    private val logger: Logger,
) {
    fun apply(config: String) {
        logger.log("Detected [AmneziaWG] config, applying Awg parameters")
        awgRepo.setIsAmneziaWGEnabled(true)
        awgRepo.setAwgConfig(config)
    }
}
