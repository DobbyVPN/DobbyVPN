package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryTrustTunnel
import com.dobby.feature.main.domain.TrustTunnelClientConfig
import com.dobby.feature.main.domain.clearTrustTunnelConfig
import kotlinx.serialization.encodeToString
import net.peanuuutz.tomlkt.Toml

internal class TrustTunnelTomlApplier(
    private val trustTunnelRepo: DobbyConfigsRepositoryTrustTunnel,
    private val logger: Logger,
) {
    // We create a TOML instance to encode the config back to a string
    private val toml = Toml {
        ignoreUnknownKeys = true
    }

    fun apply(config: TrustTunnelClientConfig): Boolean {
        logger.log("Applying generic [TrustTunnel] configuration")

        // TrustTunnel strictly requires an endpoint to connect to
        if (config.endpoint == null) {
            logger.log("Invalid [TrustTunnel]: Config is empty (no endpoint).")
            trustTunnelRepo.clearTrustTunnelConfig()
            return false
        }

        try {
            // Encode the data class back into a raw TOML string
            // because the TrustTunnel C++ engine natively consumes TOML.
            val tomlString = toml.encodeToString(config)

            logger.log("Successfully parsed TrustTunnel config")
            trustTunnelRepo.setTrustTunnelConfig(tomlString)
            return true

        } catch (e: Exception) {
            logger.log("Failed to encode TrustTunnel TOML: ${e.message}")
            trustTunnelRepo.clearTrustTunnelConfig()
            return false
        }
    }
}