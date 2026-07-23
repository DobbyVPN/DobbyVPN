package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionProfile
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.OutlineConfig
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.XrayClientConfig
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig
import com.dobby.feature.main.domain.clearXrayConfig
import com.dobby.feature.main.domain.clearTrustTunnelConfig
import com.dobby.feature.main.domain.TrustTunnelConfig
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

internal class ConnectionProfileApplier(
    private val repo: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private val outlineApplier = OutlineTomlApplier(repo, repo, repo, logger)
    private val cloakApplier = CloakTomlApplier(repo, logger)
    private val xrayApplier = XrayTomlApplier(repo, logger)
    private val trustTunnelApplier = TrustTunnelTomlApplier(repo, logger)

    fun apply(profile: ConnectionProfile): Boolean {
        logger.log(
            "[Profiles] Applying profile index=${profile.sourceIndex} " +
                "protocol=${profile.protocol} description=${profile.description ?: "(none)"}"
        )

        clearActiveProtocolFields()

        return when (profile.protocol) {
            VpnInterface.CLOAK_OUTLINE -> applyOutline(profile)
            VpnInterface.XRAY -> applyXray(profile)
            VpnInterface.TRUST_TUNNEL -> applyTrustTunnel(profile)
            VpnInterface.NONE -> {
                repo.setVpnInterface(VpnInterface.NONE)
                false
            }
        }
    }

    private fun clearActiveProtocolFields() {
        repo.clearOutlineConfig()
        repo.clearCloakConfig()
        repo.clearXrayConfig()
        repo.clearTrustTunnelConfig()
    }

    private fun applyOutline(profile: ConnectionProfile): Boolean {
        val config = runCatching {
            Toml.decodeFromString<OutlineConfig>(profile.payload)
        }.onFailure { e ->
            logger.log("[Profiles] Failed to decode Outline profile: ${e.message}")
        }.getOrNull() ?: return false

        val outlineResult = outlineApplier.apply(config) ?: return false
        val (cloakEnabled, _) = outlineResult
        cloakApplier.apply(config, cloakEnabled)
        return true
    }

    private fun applyXray(profile: ConnectionProfile): Boolean {
        val config = runCatching {
            Toml.decodeFromString<XrayClientConfig>(profile.payload)
        }.onFailure { e ->
            logger.log("[Profiles] Failed to decode Xray profile: ${e.message}")
        }.getOrNull() ?: return false

        return xrayApplier.apply(config)
    }

    private fun applyTrustTunnel(profile: ConnectionProfile): Boolean {
        val config = runCatching {
            Toml.decodeFromString<TrustTunnelConfig>(profile.payload)
        }.onFailure { e ->
            logger.log("[Profiles] Failed to decode TrustTunnel profile: ${e.message}")
        }.getOrNull() ?: return false

        return trustTunnelApplier.apply(config)
    }
}
