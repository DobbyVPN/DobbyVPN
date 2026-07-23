package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.*
import kotlinx.serialization.encodeToString
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.decodeFromString

class TomlConfigApplier(
    private val mainRepo: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private val profileManager = ConnectionProfileManager(mainRepo, logger)

    private fun preprocess(text: String): String {
        val sectionRe = Regex("^\\|([A-Za-z_][\\w-]*)\\|\\s*$", RegexOption.MULTILINE)

        return sectionRe.replace(text) { match ->
            val sectionName = match.groupValues[1]
            when (sectionName) {
                "endpoint" -> "[TrustTunnel.endpoint]"
                "socks" -> "[TrustTunnel.listener.socks]"
                else -> "[TrustTunnel.$sectionName]"
            }
        }
    }

    fun apply(connectionConfig: String): Boolean {
        logger.log("Start parseToml()")

        if (connectionConfig.isBlank()) {
            logger.log("Connection config is blank, skipping parseToml()")
            return false
        }

        if (!hasProtocolHeaders(connectionConfig)) {
            logger.log(
                "Unsupported config: expected protocol sections [[Outline]], [[Xray]], or [[TrustTunnel]]. " +
                    "Legacy single-table protocol sections are not supported."
            )
            return false
        }

        return applyConfig(preprocess(connectionConfig))
    }

    private fun applyConfig(connectionConfig: String): Boolean {
        val root = try {
            Toml.decodeFromString<TomlConfigs>(connectionConfig)
        } catch (e: Exception) {
            logger.log("Failed to parse TOML config: ${e.message}")
            return false
        }

        applyCommon(root.Telemetry, root.ExcludeIPs)

        val profiles = buildOrderedProfiles(connectionConfig, root) ?: return false
        if (profiles.isEmpty()) {
            logger.log("Unsupported config: no protocol profiles found")
            return false
        }

        val applied = profileManager.replaceProfilesAndApplyFirstAvailable(profiles)
        logger.log("Finish parseToml()")
        return applied
    }

    private fun applyCommon(telemetry: TelemetryConfig?, exclude: ExcludeIPsConfig?) {
        mainRepo.setTelemetryEndpoint(telemetry?.Endpoint ?: "")
        mainRepo.setTelemetryApiToken(telemetry?.ApiToken ?: "")

        if (exclude?.IPs != null && exclude.IPs.isNotEmpty()) {
            val cidrsString = exclude.IPs.joinToString(" ")
            val sample = exclude.IPs.take(5).joinToString(" ")
            logger.log("Applying ExcludeIPs: count=${exclude.IPs.size} size=${cidrsString.length} sample=$sample")
            mainRepo.setGeoRoutingConf(cidrsString)
        } else {
            logger.log("ExcludeIPs not found or empty → clearing routing")
            mainRepo.setGeoRoutingConf("")
        }
    }

    private fun hasProtocolHeaders(connectionConfig: String): Boolean =
        protocolHeaderRegex.containsMatchIn(connectionConfig)

    private fun buildOrderedProfiles(
        connectionConfig: String,
        root: TomlConfigs
    ): List<ConnectionProfile>? {
        val headers = protocolHeaderRegex.findAll(connectionConfig)
            .map { it.groupValues[1] }
            .toList()

        val headerCounts = headers.groupingBy { it }.eachCount()
        if (
            headerCounts.getOrElse("Outline") { 0 } != root.Outline.size ||
            headerCounts.getOrElse("Xray") { 0 } != root.Xray.size ||
            headerCounts.getOrElse("TrustTunnel") { 0 } != root.TrustTunnel.size
        ) {
            logger.log(
                "Invalid TOML config: protocol header counts do not match parsed blocks " +
                    "headers=$headerCounts parsed={Outline=${root.Outline.size}, Xray=${root.Xray.size}, TrustTunnel=${root.TrustTunnel.size}}"
            )
            return null
        }

        var outlineIndex = 0
        var xrayIndex = 0
        var trustTunnelIndex = 0

        return headers.mapIndexed { index, name ->
            when (name) {
                "Outline" -> root.Outline[outlineIndex++].let {
                    ConnectionProfile(
                        protocol = VpnInterface.CLOAK_OUTLINE,
                        description = it.Description,
                        sourceIndex = index,
                        payload = Toml.encodeToString(it)
                    )
                }
                "Xray" -> root.Xray[xrayIndex++].let {
                    ConnectionProfile(
                        protocol = VpnInterface.XRAY,
                        description = it.Description,
                        sourceIndex = index,
                        payload = Toml.encodeToString(it)
                    )
                }
                "TrustTunnel" -> root.TrustTunnel[trustTunnelIndex++].let {
                    ConnectionProfile(
                        protocol = VpnInterface.TRUST_TUNNEL,
                        description = null,
                        sourceIndex = index,
                        payload = Toml.encodeToString(it)
                    )
                }
                else -> error("Unexpected protocol header: $name")
            }
        }
    }

    private companion object {
        val protocolHeaderRegex = Regex("""(?m)^\s*\[\[\s*(Outline|Xray|TrustTunnel)\s*]]""")
    }

}
