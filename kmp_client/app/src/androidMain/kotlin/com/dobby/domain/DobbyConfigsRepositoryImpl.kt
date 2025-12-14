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

    override fun setPrefixOutline(newPrefix: String) {
        prefs.edit().putString("PrefixOutlineKey", newPrefix).apply().also {
            AndroidLog("DOBBY_TAG", "setPrefixOutline, size = ${newPrefix.length}")
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

    override fun getPrefixOutline(): String {
        return (prefs.getString("PrefixOutlineKey", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getPrefixOutline, size = ${it.length}")
        }
    }

    override fun setDataPrefixOutline(newDataPrefix: String) {
        prefs.edit().putString("DataPrefixOutlineKey", newDataPrefix).apply().also {
            AndroidLog("DOBBY_TAG", "setDataPrefixOutline, size = ${newDataPrefix.length}")
        }
    }

    override fun getDataPrefixOutline(): String {
        return (prefs.getString("DataPrefixOutlineKey", "") ?: "").also {
            AndroidLog("DOBBY_TAG", "getDataPrefixOutline, size = ${it.length}")
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
