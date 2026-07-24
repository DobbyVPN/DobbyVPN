package com.dobby.domain

import android.content.SharedPreferences
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import android.util.Log.i as AndroidLog

internal class DobbyConfigsRepositoryImpl(
    private val prefs: SharedPreferences
) : DobbyConfigsRepository {
    private val secrets = AndroidKeystoreSecretStore(prefs)

    init {
        secrets.migrate(SENSITIVE_STRING_KEYS)
    }

    private fun secret(name: String): String = secrets.read(name)
    private fun putSecret(name: String, value: String) {
        check(secrets.write(name, value)) { "secure configuration write failed" }
    }

    override fun getVpnInterface(): VpnInterface {
        val prefsResult = secret("vpnInterface").ifEmpty { VpnInterface.DEFAULT_VALUE.toString() }
        AndroidLog("DOBBY_TAG", "getVpnInterface: $prefsResult")

        return VpnInterface.valueOf(prefsResult)
    }

    override fun setVpnInterface(vpnInterface: VpnInterface) {
        putSecret("vpnInterface", vpnInterface.toString()).also {
            AndroidLog("DOBBY_TAG", "setVpnInterface: $vpnInterface")
        }
    }

    override fun getConnectionURL(): String {
        return secret("сonnectionURL").also {
            AndroidLog("DOBBY_TAG", "getConnectionURL, size = ${it.length}")
        }
    }

    override fun setConnectionURL(connectionURL: String) {
        putSecret("сonnectionURL", connectionURL).also {
            AndroidLog("DOBBY_TAG", "setConnectionURL, size = ${connectionURL.length}")
        }
    }

    override fun getCloakConfig(): String {
        return secret("cloakConfig").also {
            AndroidLog("DOBBY_TAG", "getCloakConfig, size = ${it.length}")
        }
    }

    override fun setCloakConfig(newConfig: String) {
        putSecret("cloakConfig", newConfig).also {
            AndroidLog("DOBBY_TAG", "setCloakConfig, size = ${newConfig.length}")
        }
    }

    override fun getConnectionConfig(): String {
        return secret("сonnectionConfig").also {
            AndroidLog("DOBBY_TAG", "getConnectionConfig, size = ${it.length}")
        }
    }

    override fun setConnectionConfig(connectionConfig: String) {
        putSecret("сonnectionConfig", connectionConfig).also {
            AndroidLog("DOBBY_TAG", "setConnectionConfig, size = ${connectionConfig.length}")
        }
    }

    override fun getConnectionProfiles(): String {
        return secret("connectionProfiles").also {
            AndroidLog("DOBBY_TAG", "getConnectionProfiles, size = ${it.length}")
        }
    }

    override fun setConnectionProfiles(connectionProfiles: String) {
        putSecret("connectionProfiles", connectionProfiles).also {
            AndroidLog("DOBBY_TAG", "setConnectionProfiles, size = ${connectionProfiles.length}")
        }
    }

    override fun getActiveConnectionProfileIndex(): Int {
        return prefs.getInt("activeConnectionProfileIndex", 0).also {
            AndroidLog("DOBBY_TAG", "getActiveConnectionProfileIndex = $it")
        }
    }

    override fun setActiveConnectionProfileIndex(index: Int) {
        prefs.edit().putInt("activeConnectionProfileIndex", index).apply().also {
            AndroidLog("DOBBY_TAG", "setActiveConnectionProfileIndex = $index")
        }
    }

    override fun getIsCloakEnabled(): Boolean {
        return prefs.getBoolean("isCloakEnabled", false).also {
            AndroidLog("DOBBY_TAG", "getIsCloakEnabled: $it")
        }
    }

    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        prefs.edit().putBoolean("isCloakEnabled", isCloakEnabled).apply().also {
            AndroidLog("DOBBY_TAG", "setIsCloakEnabled: $isCloakEnabled")
        }
    }

    override fun getCloakLocalPort(): Int {
        return prefs.getInt("cloakLocalPort", 1984).also {
            AndroidLog("DOBBY_TAG", "getCloakLocalPort: $it")
        }
    }

    override fun setCloakLocalPort(port: Int) {
        prefs.edit().putInt("cloakLocalPort", port).apply().also {
            AndroidLog("DOBBY_TAG", "setCloakLocalPort: $port")
        }
    }

    override fun setServerPort(newConfig: String) {
        putSecret("ServerPortKey", newConfig).also {
            AndroidLog("DOBBY_TAG", "setServerPort, size = ${newConfig.length}")
        }
    }

    override fun setMethodPasswordOutline(newConfig: String) {
        putSecret("MethodPasswordOutlineKey", newConfig).also {
            AndroidLog("DOBBY_TAG", "setMethodPasswordOutline, size = ${newConfig.length}")
        }
    }

    override fun getServerPort(): String {
        return secret("ServerPortKey").also {
            AndroidLog("DOBBY_TAG", "getServerPort, size = ${it.length}")
        }
    }

    override fun getMethodPasswordOutline(): String {
        return secret("MethodPasswordOutlineKey").also {
            AndroidLog("DOBBY_TAG", "getMethodPasswordOutline, size = ${it.length}")
        }
    }

    override fun getIsOutlineEnabled(): Boolean {
        return prefs.getBoolean("isOutlineEnabled", false).also {
            AndroidLog("DOBBY_TAG", "getIsOutlineEnabled = $it")
        }
    }

    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        prefs.edit().putBoolean("isOutlineEnabled", isOutlineEnabled).apply().also {
            AndroidLog("DOBBY_TAG", "setIsOutlineEnabled = $isOutlineEnabled")
        }
    }

    override fun getPrefixOutline(): String {
        return secret("PrefixOutlineKey").also {
            AndroidLog("DOBBY_TAG", "getPrefixOutline, size = ${it.length}")
        }
    }

    override fun setPrefixOutline(prefix: String) {
        putSecret("PrefixOutlineKey", prefix).also {
            AndroidLog("DOBBY_TAG", "setPrefixOutline, size = ${prefix.length}")
        }
    }

    override fun getTcpPathOutline(): String {
        return secret("TcpPathOutlineKey").also {
            AndroidLog("DOBBY_TAG", "getTcpPathOutline, size = ${it.length}")
        }
    }

    override fun setTcpPathOutline(tcpPath: String) {
        putSecret("TcpPathOutlineKey", tcpPath).also {
            AndroidLog("DOBBY_TAG", "setTcpPathOutline, size = ${tcpPath.length}")
        }
    }

    override fun getIsWebsocketEnabled(): Boolean {
        return prefs.getBoolean("isWebsocketEnabled", false).also {
            AndroidLog("DOBBY_TAG", "getIsWebsocketEnabled = $it")
        }
    }

    override fun setIsWebsocketEnabled(enabled: Boolean) {
        prefs.edit().putBoolean("isWebsocketEnabled", enabled).apply().also {
            AndroidLog("DOBBY_TAG", "setIsWebsocketEnabled = $enabled")
        }
    }

    override fun getUdpPathOutline(): String {
        return secret("UdpPathOutlineKey").also {
            AndroidLog("DOBBY_TAG", "getUdpPathOutline, size = ${it.length}")
        }
    }

    override fun setUdpPathOutline(udpPath: String) {
        putSecret("UdpPathOutlineKey", udpPath).also {
            AndroidLog("DOBBY_TAG", "setUdpPathOutline, size = ${udpPath.length}")
        }
    }

    override fun getXrayConfig(): String {
        return secret("xrayConfig").also {
            AndroidLog("DOBBY_TAG", "getXrayConfig, size = ${it.length}")
        }
    }

    override fun setXrayConfig(newConfig: String) {
        putSecret("xrayConfig", newConfig).also {
            AndroidLog("DOBBY_TAG", "setXrayConfig, size = ${newConfig.length}")
        }
    }

    override fun getIsXrayEnabled(): Boolean {
        return prefs.getBoolean("isXrayEnabled", false).also {
            AndroidLog("DOBBY_TAG", "getIsXrayEnabled = $it")
        }
    }

    override fun setIsXrayEnabled(isXrayEnabled: Boolean) {
        if (isXrayEnabled) {
            setVpnInterface(VpnInterface.XRAY) // TODO (find other place for this command?)
        } else {
            setVpnInterface(VpnInterface.DEFAULT_VALUE)
        }
        prefs.edit().putBoolean("isXrayEnabled", isXrayEnabled).apply().also {
            AndroidLog("DOBBY_TAG", "setIsXrayEnabled = $isXrayEnabled")
        }
    }

    override fun getTrustTunnelConfig(): String {
        return secret("trustTunnelConfig").also {
            AndroidLog("DOBBY_TAG", "getTrustTunnelConfig, size = ${it.length}")
        }
    }

    override fun setTrustTunnelConfig(config: String) {
        putSecret("trustTunnelConfig", config).also {
            AndroidLog("DOBBY_TAG", "setTrustTunnelConfig, size = ${config.length}")
        }
    }

    override fun getIsTrustTunnelEnabled(): Boolean {
        return prefs.getBoolean("isTrustTunnelEnabled", false).also {
            AndroidLog("DOBBY_TAG", "getIsTrustTunnelEnabled = $it")
        }
    }

    override fun setIsTrustTunnelEnabled(isTrustTunnelEnabled: Boolean) {
        if (isTrustTunnelEnabled) {
            setVpnInterface(VpnInterface.TRUST_TUNNEL)
        } else {
            setVpnInterface(VpnInterface.DEFAULT_VALUE)
        }
        prefs.edit().putBoolean("isTrustTunnelEnabled", isTrustTunnelEnabled).apply().also {
            AndroidLog("DOBBY_TAG", "setIsTrustTunnelEnabled = $isTrustTunnelEnabled")
        }
    }

    override fun couldStart(): Boolean {
        return true
    }

    override fun getTelemetryEndpoint(): String {
        return secret("telemetryEndpoint").also {
            AndroidLog("DOBBY_TAG", "getTelemetryEndpoint, size = ${it.length}")
        }
    }

    override fun setTelemetryEndpoint(endpoint: String) {
        putSecret("telemetryEndpoint", endpoint).also {
            AndroidLog("DOBBY_TAG", "setTelemetryEndpoint, size = ${endpoint.length}")
        }
    }

    override fun getTelemetryApiToken(): String {
        return secret("telemetryApiToken").also {
            AndroidLog("DOBBY_TAG", "getTelemetryApiToken, size = ${it.length}")
        }
    }

    override fun setTelemetryApiToken(token: String) {
        putSecret("telemetryApiToken", token).also {
            AndroidLog("DOBBY_TAG", "setTelemetryApiToken, size = ${token.length}")
        }
    }

    override fun getTelemetryAttributes(): String {
        return secret("telemetryAttributes").also {
            AndroidLog("DOBBY_TAG", "getTelemetryAttributes, size = ${it.length}")
        }
    }

    override fun setTelemetryAttributes(config: String) {
        putSecret("telemetryAttributes", config).also {
            AndroidLog("DOBBY_TAG", "setTelemetryAttributes, size = ${config.length}")
        }
    }

    override fun getGeoRoutingConf(): String {
        return secret("geoRoutingConf").also {
            AndroidLog("DOBBY_TAG", "geoRoutingConf, len(geoRoutingConf) = ${it.length}")
        }
    }

    override fun setGeoRoutingConf(geoRoutingConf: String) {
        putSecret("geoRoutingConf", geoRoutingConf).also {
            AndroidLog("DOBBY_TAG", "setGeoRoutingConf, size = ${geoRoutingConf.length}")
        }
    }

    private companion object {
        val SENSITIVE_STRING_KEYS = setOf(
            "vpnInterface",
            "сonnectionURL",
            "cloakConfig",
            "сonnectionConfig",
            "connectionProfiles",
            "ServerPortKey",
            "MethodPasswordOutlineKey",
            "PrefixOutlineKey",
            "TcpPathOutlineKey",
            "UdpPathOutlineKey",
            "xrayConfig",
            "trustTunnelConfig",
            "telemetryEndpoint",
            "telemetryApiToken",
            "telemetryAttributes",
            "geoRoutingConf",
        )
    }
}
