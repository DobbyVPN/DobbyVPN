package com.dobby.feature.main.domain.config

import kotlinx.serialization.Serializable
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

    @Serializable
    private data class NestedEndpoint(
        val hostname: String? = null,
        val addresses: List<String>? = null,
        val custom_sni: String? = null,
        val username: String? = null,
        val password: String? = null,
        val client_random: String? = null,
        val skip_verification: Boolean? = null,
        val upstream_protocol: String? = null,
        val anti_dpi: Boolean? = null,
        val dns_upstreams: List<String>? = null
    )

    @Serializable
    private data class NestedSocks(
        val address: String? = null
    )

    @Serializable
    private data class NestedListener(
        val socks: NestedSocks? = null
    )

    @Serializable
    private data class NestedTrustTunnelConfig(
        val loglevel: String? = null,
        val vpn_mode: String? = null,
        val killswitch_enabled: Boolean? = null,
        val killswitch_allow_ports: String? = null,
        val post_quantum_group_enabled: Boolean? = null,
        val exclusions: List<String>? = null,
        val endpoint: NestedEndpoint? = null,
        val listener: NestedListener? = null
    )

    fun apply(config: TrustTunnelConfig): Boolean {
        logger.log("Applying [TrustTunnel] configuration")

        val nestedConfig = NestedTrustTunnelConfig(
            loglevel = config.loglevel,
            vpn_mode = config.vpn_mode ?: config.vpnMode,
            killswitch_enabled = config.killswitch_enabled ?: config.killswitchEnabled,
            killswitch_allow_ports = config.killswitch_allow_ports ?: config.killswitchAllowPorts,
            post_quantum_group_enabled = config.post_quantum_group_enabled ?: config.postQuantumGroupEnabled,
            exclusions = config.exclusions.takeIf { it.isNotEmpty() },
            endpoint = NestedEndpoint(
                hostname = config.endpoint_hostname,
                addresses = config.endpoint_addresses.takeIf { it.isNotEmpty() },
                custom_sni = config.endpoint_custom_sni,
                username = config.endpoint_username,
                password = config.endpoint_password,
                client_random = config.endpoint_client_random,
                skip_verification = config.endpoint_skip_verification,
                upstream_protocol = config.endpoint_upstream_protocol,
                anti_dpi = config.endpoint_anti_dpi,
                dns_upstreams = config.endpoint_dns_upstreams.takeIf { it.isNotEmpty() }
            ),
            listener = config.listener_socks_address?.let { NestedListener(NestedSocks(it)) }
        )

        val tomlString = try {
            toml.encodeToString(NestedTrustTunnelConfig.serializer(), nestedConfig)
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
        val hostname = config.endpoint_hostname

        if (hostname != null) {
            trustTunnelRepo.setServerPort(hostname)
            logger.log("Extracted TrustTunnel Server IP/Hostname: $hostname")
        } else {
            logger.log("Could not extract TrustTunnel Server IP/Hostname from endpoint.")
        }
    }
}
