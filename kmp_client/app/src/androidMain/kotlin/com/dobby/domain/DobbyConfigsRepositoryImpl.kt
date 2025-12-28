package com.dobby.domain

import android.content.SharedPreferences
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import android.util.Log.i as AndroidLog
import com.dobby.outline.OutlineGo

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

    override fun setServerPortOutline(newConfig: String) {
        prefs.edit().putString("ServerPortOutlineKey", newConfig).apply().also {
            AndroidLog("DOBBY_TAG", "setServerPortOutline, size = ${newConfig.length}")
        }
    }

    override fun setMethodPasswordOutline(newConfig: String) {
        prefs.edit().putString("MethodPasswordOutlineKey", newConfig).apply().also {
            AndroidLog("DOBBY_TAG", "setMethodPasswordOutline, size = ${newConfig.length}")
        }
    }

    override fun getServerPortOutline(): String {
        return (prefs.getString("ServerPortOutlineKey", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getServerPortOutline, size = ${it.length}")
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

    override fun getAwgConfig(): String {
        return (prefs.getString("awgConfig", DEFAULT_AWG_CONFIG) ?: "").also {
            AndroidLog("DOBBY_TAG", "getAwgConfig, size = ${it.length}")
        }
    }

    override fun setAwgConfig(newConfig: String) {
        prefs.edit().putString("awgConfig", newConfig).apply().also {
            AndroidLog("DOBBY_TAG", "setAwgConfig, size = ${newConfig.length}")
        }
    }

    override fun getIsAmneziaWGEnabled(): Boolean {
        return prefs.getBoolean("isAmneziaWGEnabled", false).also {
            AndroidLog("DOBBY_TAG", "getIsAmneziaWGEnabled = $it")
        }
    }

    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) {
        prefs.edit().putBoolean("isAmneziaWGEnabled", isAmneziaWGEnabled).apply().also {
            AndroidLog("DOBBY_TAG", "setIsAmneziaWGEnabled = $isAmneziaWGEnabled")
        }
    }

    override fun couldStart(): Boolean {
        return true
    }

    override fun getIsUserInitStop(): Boolean {
        return prefs.getBoolean("isUserInitStop", true).also {
            AndroidLog("DOBBY_TAG", "getIsUserInitStop = $it")
        }
    }

    override fun setIsUserInitStop(isUserInitStop: Boolean) {
        prefs.edit().putBoolean("isUserInitStop", isUserInitStop).apply().also {
            AndroidLog("DOBBY_TAG", "setIsUserInitStop = $isUserInitStop")
        }
    }

    companion object {
        const val DEFAULT_AWG_CONFIG = """[Interface]
PrivateKey = <...>
Address = <...>
DNS = 8.8.8.8
Jc = 0
Jmin = 0
Jmax = 0
S1 = 0
S2 = 0
H1 = 1
H2 = 2
H3 = 3
H4 = 4

[Peer]
PublicKey = <...>
Endpoint = <...>
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 60
"""
    }
}
