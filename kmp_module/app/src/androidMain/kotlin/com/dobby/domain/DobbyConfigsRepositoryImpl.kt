package com.dobby.domain

import android.content.SharedPreferences
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import android.util.Log.i as AndroidLog
import androidx.core.content.edit

internal class DobbyConfigsRepositoryImpl(
    private val prefs: SharedPreferences
) : DobbyConfigsRepository {
    override fun getVpnInterface(): VpnInterface {
        val prefsResult = prefs.getString("vpnInterface", VpnInterface.DEFAULT_VALUE.toString())
            ?: VpnInterface.DEFAULT_VALUE.toString()
        AndroidLog("DOBBY_TAG", "getVpnInterface: $prefsResult")

        return VpnInterface.valueOf(prefsResult)
    }

    override fun setVpnInterface(vpnInterface: VpnInterface) {
        prefs.edit().putString("vpnInterface", vpnInterface.toString()).apply().also {
            AndroidLog("DOBBY_TAG", "setVpnInterface: $vpnInterface")
        }
    }

    override fun getConnectionURL(): String {
        return (prefs.getString("сonnectionURL", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getConnectionURL, url = ${it}")
        }
    }

    override fun setConnectionURL(connectionURL: String) {
        prefs.edit().putString("сonnectionURL", connectionURL).apply().also {
            AndroidLog("DOBBY_TAG", "setConnectionURL, url = ${connectionURL}")
        }
    }

    override fun getCloakConfig(): String {
        return (prefs.getString("cloakConfig", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getCloakConfig, size = ${it.length}")
        }
    }

    override fun setCloakConfig(newConfig: String) {
        prefs.edit().putString("cloakConfig", newConfig).apply().also {
            AndroidLog("DOBBY_TAG", "setCloakConfig, size = ${newConfig.length}")
        }
    }

    override fun getConnectionConfig(): String {
        return (prefs.getString("сonnectionConfig", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getConnectionConfig, config = ${it}")
        }
    }

    override fun setConnectionConfig(connectionConfig: String) {
        prefs.edit().putString("сonnectionConfig", connectionConfig).apply().also {
            AndroidLog("DOBBY_TAG", "setConnectionConfig, config = ${connectionConfig}")
        }
    }

    override fun getConnectionProfiles(): String {
        return (prefs.getString("connectionProfiles", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getConnectionProfiles, size = ${it.length}")
        }
    }

    override fun setConnectionProfiles(connectionProfiles: String) {
        prefs.edit().putString("connectionProfiles", connectionProfiles).apply().also {
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
        prefs.edit().putString("ServerPortKey", newConfig).apply().also {
            AndroidLog("DOBBY_TAG", "setServerPort, size = ${newConfig.length}")
        }
    }

    override fun setMethodPasswordOutline(newConfig: String) {
        prefs.edit().putString("MethodPasswordOutlineKey", newConfig).apply().also {
            AndroidLog("DOBBY_TAG", "setMethodPasswordOutline, size = ${newConfig.length}")
        }
    }

    override fun getServerPort(): String {
        return (prefs.getString("ServerPortKey", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getServerPort, size = ${it.length}")
        }
    }

    override fun getMethodPasswordOutline(): String {
        return (prefs.getString("MethodPasswordOutlineKey", "") ?: "").also {
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
        return (prefs.getString("PrefixOutlineKey", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getPrefixOutline, value = $it")
        }
    }

    override fun setPrefixOutline(prefix: String) {
        prefs.edit().putString("PrefixOutlineKey", prefix).apply().also {
            AndroidLog("DOBBY_TAG", "setPrefixOutline, value = $prefix")
        }
    }

    override fun getTcpPathOutline(): String {
        return (prefs.getString("TcpPathOutlineKey", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getTcpPathOutline, value = $it")
        }
    }

    override fun setTcpPathOutline(tcpPath: String) {
        prefs.edit().putString("TcpPathOutlineKey", tcpPath).apply().also {
            AndroidLog("DOBBY_TAG", "setTcpPathOutline, value = $tcpPath")
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
        return (prefs.getString("UdpPathOutlineKey", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getUdpPathOutline, value = $it")
        }
    }

    override fun setUdpPathOutline(udpPath: String) {
        prefs.edit().putString("UdpPathOutlineKey", udpPath).apply().also {
            AndroidLog("DOBBY_TAG", "setUdpPathOutline, value = $udpPath")
        }
    }

    override fun getXrayConfig(): String {
        return (prefs.getString("xrayConfig", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getXrayConfig, size = ${it.length}")
        }
    }

    override fun setXrayConfig(newConfig: String) {
        prefs.edit().putString("xrayConfig", newConfig).apply().also {
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
        return (prefs.getString("trustTunnelConfig", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getTrustTunnelConfig, size = ${it.length}")
        }
    }

    override fun setTrustTunnelConfig(config: String) {
        prefs.edit().putString("trustTunnelConfig", config).apply().also {
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
        return (prefs.getString("telemetryEndpoint", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getTelemetryEndpoint, size = ${it.length}")
        }
    }

    override fun setTelemetryEndpoint(endpoint: String) {
        prefs.edit().putString("telemetryEndpoint", endpoint).apply().also {
            AndroidLog("DOBBY_TAG", "setTelemetryEndpoint, size = ${endpoint.length}")
        }
    }

    override fun getTelemetryApiToken(): String {
        return (prefs.getString("telemetryApiToken", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getTelemetryApiToken, size = ${it.length}")
        }
    }

    override fun setTelemetryApiToken(token: String) {
        prefs.edit().putString("telemetryApiToken", token).apply().also {
            AndroidLog("DOBBY_TAG", "setTelemetryApiToken, size = ${token.length}")
        }
    }

    override fun getTelemetryAttributes(): String {
        return (prefs.getString("telemetryAttributes", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getTelemetryAttributes, size = ${it.length}")
        }
    }

    override fun setTelemetryAttributes(config: String) {
        prefs.edit().putString("telemetryAttributes", config).apply().also {
            AndroidLog("DOBBY_TAG", "setTelemetryAttributes, size = ${config.length}")
        }
    }

    override fun getGeoRoutingConf(): String {
        return (prefs.getString("geoRoutingConf", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "geoRoutingConf, len(geoRoutingConf) = ${it.length}")
        }
    }

    override fun setGeoRoutingConf(geoRoutingConf: String) {
        prefs.edit { putString("geoRoutingConf", geoRoutingConf) }.also {
            AndroidLog("DOBBY_TAG", "geoRoutingConf = $geoRoutingConf")
        }
    }
}
