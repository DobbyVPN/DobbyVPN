package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryTrustTunnel
import com.dobby.feature.main.domain.TrustTunnelConfig

internal class TrustTunnelTomlApplier(
    private val trustTunnelRepo: DobbyConfigsRepositoryTrustTunnel,
    private val logger: Logger,
) {
    fun apply(config: TrustTunnelConfig): Boolean {
        logger.log("Applying [TrustTunnel] configuration")

        if (config.Config.isBlank()) {
            logger.log("Invalid [TrustTunnel]: Config string is blank.")
            trustTunnelRepo.setTrustTunnelConfig("")
            trustTunnelRepo.setIsTrustTunnelEnabled(false)
            return false
        }

        trustTunnelRepo.setTrustTunnelConfig(config.Config)
        trustTunnelRepo.setIsTrustTunnelEnabled(true)

        logger.log("TrustTunnel config applied successfully.")
        return true
    }
}
