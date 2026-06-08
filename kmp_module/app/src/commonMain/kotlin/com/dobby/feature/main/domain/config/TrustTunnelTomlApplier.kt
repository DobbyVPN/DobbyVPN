package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryTrustTunnel
import com.dobby.feature.main.domain.TrustTunnelConfig
import net.peanuuutz.tomlkt.Toml

internal class TrustTunnelTomlApplier(
    private val trustTunnelRepo: DobbyConfigsRepositoryTrustTunnel,
    private val logger: Logger,
) {
    private val toml = Toml {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    fun apply(config: TrustTunnelConfig): Boolean {
        logger.log("Applying [TrustTunnel] configuration")

        val tomlString = try {
            toml.encodeToString(TrustTunnelConfig.serializer(), config)
        } catch (e: Exception) {
            logger.log("Failed to encode TrustTunnel config: ${e.message}")
            trustTunnelRepo.setTrustTunnelConfig("")
            trustTunnelRepo.setIsTrustTunnelEnabled(false)
            return false
        }

        if (tomlString.isBlank()) {
            logger.log("Invalid [TrustTunnel]: Config string is blank.")
            trustTunnelRepo.setTrustTunnelConfig("")
            trustTunnelRepo.setIsTrustTunnelEnabled(false)
            return false
        }

        trustTunnelRepo.setTrustTunnelConfig(tomlString)
        trustTunnelRepo.setIsTrustTunnelEnabled(true)

        extractAndSetServerPort(config)

        logger.log("TrustTunnel config applied successfully.")
        return true
    }

    private fun extractAndSetServerPort(config: TrustTunnelConfig) {
        val endpointTable = config.endpoint as? net.peanuuutz.tomlkt.TomlTable
        val hostname = (endpointTable?.get("hostname") as? net.peanuuutz.tomlkt.TomlLiteral)?.content

        if (hostname != null) {
            trustTunnelRepo.setServerPort(hostname)
            logger.log("Extracted TrustTunnel Server IP/Hostname: $hostname")
        } else {
            logger.log("Could not extract TrustTunnel Server IP/Hostname from endpoint.")
        }
    }
}
