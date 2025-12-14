package com.dobby.domain

import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import java.util.prefs.Preferences
import interop.VPNLibraryLoader

internal class DobbyConfigsRepositoryImpl(
    private val prefs: Preferences = Preferences.systemRoot(),
    private val vpnLibrary: VPNLibraryLoader,
) : DobbyConfigsRepository {

    override fun getVpnInterface(): VpnInterface {
        val prefsResult = prefs.get("vpnInterface", VpnInterface.DEFAULT_VALUE.toString())
            ?: VpnInterface.DEFAULT_VALUE.toString()

        return VpnInterface.valueOf(prefsResult)
    }

    override fun setVpnInterface(vpnInterface: VpnInterface) {
        prefs.put("vpnInterface", vpnInterface.toString())
    }

    override fun getConnectionURL(): String {
        return prefs.get("connectionURL", "")
    }

    override fun setConnectionURL(connectionURL: String) {
        prefs.put("connectionURL", connectionURL)
    }

    override fun getConnectionConfig(): String {
        return prefs.get("connectionConfig", "")
    }

    override fun setConnectionConfig(connectionConfig: String) {
        prefs.put("connectionConfig", connectionConfig)
    }

    override fun getCloakConfig(): String {
        return prefs.get("cloakConfig", "")
    }

    override fun setCloakConfig(newConfig: String) {
        prefs.put("cloakConfig", newConfig)
    }

    override fun getIsCloakEnabled(): Boolean {
        return prefs.get("isCloakEnabled", "false").equals("true")
    }

    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        prefs.put("isCloakEnabled", isCloakEnabled.toString())
    }

    override fun setServerPortOutline(newConfig: String) {
        prefs.put("ServerPortOutlineKey", newConfig)
    }

    override fun setMethodPasswordOutline(newConfig: String) {
        prefs.put("MethodPasswordOutlineKey", newConfig)
    }

    override fun setPrefixOutline(newPrefix: String) {
        prefs.put("PrefixOutlineKey", newPrefix)
    }

    override fun getServerPortOutline(): String {
        return prefs.get("ServerPortOutlineKey", "")
    }

    override fun getMethodPasswordOutline(): String {
        return prefs.get("MethodPasswordOutlineKey", "")
    }

    override fun getPrefixOutline(): String {
        return prefs.get("PrefixOutlineKey", "")
    }

    override fun setDataPrefixOutline(newDataPrefix: String) {
        prefs.put("DataPrefixOutlineKey", newDataPrefix)
    }

    override fun getDataPrefixOutline(): String {
        return prefs.get("DataPrefixOutlineKey", "")
    }

    override fun getIsOutlineEnabled(): Boolean {
        return prefs.get("isOutlineEnabled", "false").equals("true")
    }

    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        prefs.put("isOutlineEnabled", isOutlineEnabled.toString())
    }

    override fun getAwgConfig(): String {
        return prefs.get("awgConfig", DEFAULT_AWG_CONFIG)
    }

    override fun setAwgConfig(newConfig: String) {
        prefs.put("awgConfig", newConfig)
    }

    override fun getIsAmneziaWGEnabled(): Boolean {
        return prefs.get("isAmneziaWGEnabled", "false").equals("true")
    }

    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) {
        prefs.put("isAmneziaWGEnabled", isAmneziaWGEnabled.toString())
    }

    override fun couldStart(): Boolean {
        return vpnLibrary.couldStart()
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
