package com.dobby.domain

import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import java.util.prefs.Preferences

internal class DobbyConfigsRepositoryImpl(
    private val prefs: Preferences = Preferences.systemRoot()
) : DobbyConfigsRepository {

    override fun getVpnInterface(): VpnInterface {
        val prefsResult = prefs.get("vpnInterface", VpnInterface.DEFAULT_VALUE.toString())
            ?: VpnInterface.DEFAULT_VALUE.toString()

        return VpnInterface.valueOf(prefsResult)
    }

    override fun setVpnInterface(vpnInterface: VpnInterface) {
        prefs.put("vpnInterface", vpnInterface.toString())
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

    override fun getOutlineKey(): String {
        return prefs.get("outlineApiKey", "")
    }

    override fun setOutlineKey(newOutlineKey: String) {
        prefs.put("outlineApiKey", newOutlineKey)
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
