package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.AmneziaWGConfig
import com.dobby.feature.main.domain.ConnectionProfile
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.OutlineConfig
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.XrayClientConfig
import com.dobby.feature.main.domain.clearAwgConfig
import com.dobby.feature.main.domain.clearCloakConfig
import com.dobby.feature.main.domain.clearOutlineConfig
import com.dobby.feature.main.domain.clearXrayConfig
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

internal class ConnectionProfileApplier(
    private val repo: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private val serverResolver = ProfileServerResolver(logger)
    private val outlineApplier = OutlineTomlApplier(repo, repo, repo, logger)
    private val cloakApplier = CloakTomlApplier(repo, logger)
    private val awgApplier = AmneziaWGTomlApplier(repo, repo, logger)
    private val xrayApplier = XrayTomlApplier(repo, logger)

    fun apply(profile: ConnectionProfile): Boolean {
        logger.log(
            "[Profiles] Applying profile index=${profile.sourceIndex} " +
                "protocol=${profile.protocol} description=${profile.description ?: "(none)"}"
        )

        clearActiveProtocolFields()

        return when (profile.protocol) {
            VpnInterface.CLOAK_OUTLINE -> applyOutline(profile)
            VpnInterface.AMNEZIA_WG -> applyAwg(profile)
            VpnInterface.XRAY -> applyXray(profile)
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
        repo.clearAwgConfig()
    }

    private fun applyOutline(profile: ConnectionProfile): Boolean {
        val config = runCatching {
            Toml.decodeFromString<OutlineConfig>(profile.payload)
        }.onFailure { e ->
            logger.log("[Profiles] Failed to decode Outline profile: ${e.message}")
        }.getOrNull() ?: return false

        val runtimeConfig = prepareOutlineConfig(config) ?: return false

        val outlineResult = outlineApplier.apply(runtimeConfig) ?: return false
        val (cloakEnabled, _) = outlineResult
        cloakApplier.apply(runtimeConfig, cloakEnabled)
        return true
    }

    private fun prepareOutlineConfig(config: OutlineConfig): OutlineConfig? {
        val server = config.Server?.trim().orEmpty()
        val cloakEnabled = config.Cloak == true

        return if (cloakEnabled) {
            val remoteHost = config.RemoteHost?.trim()?.takeIf { it.isNotEmpty() } ?: server
            if (remoteHost.isEmpty()) return config
            val resolvedRemoteHost = serverResolver.resolveIpv4(remoteHost, "Outline/Cloak remote host") ?: return null
            config.copy(
                RemoteHost = resolvedRemoteHost,
                ServerName = config.ServerName?.trim()?.takeIf { it.isNotEmpty() } ?: server,
                CDNOriginHost = config.CDNOriginHost?.trim()?.takeIf { it.isNotEmpty() } ?: server,
            )
        } else {
            if (server.isEmpty()) return config
            val resolvedServer = serverResolver.resolveIpv4(server, "Outline server") ?: return null
            config.copy(
                Server = resolvedServer,
                ServerName = config.ServerName?.trim()?.takeIf { it.isNotEmpty() } ?: server,
            )
        }
    }

    private fun applyAwg(profile: ConnectionProfile): Boolean {
        val config = runCatching {
            Toml.decodeFromString<AmneziaWGConfig>(profile.payload)
        }.onFailure { e ->
            logger.log("[Profiles] Failed to decode AmneziaWG profile: ${e.message}")
        }.getOrNull() ?: return false

        awgApplier.apply(config)
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
}
