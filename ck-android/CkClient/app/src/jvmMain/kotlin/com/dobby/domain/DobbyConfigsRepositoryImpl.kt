package com.dobby.domain

import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface

internal class DobbyConfigsRepositoryImpl() : DobbyConfigsRepository {
    override fun getVpnInterface(): VpnInterface {
        return VpnInterface.valueOf(VpnInterface.DEFAULT_VALUE.toString())
    }

    override fun setVpnInterface(vpnInterface: VpnInterface) {
    }

    override fun getCloakConfig(): String {
        return "CloakConfig"
    }

    override fun setCloakConfig(newConfig: String) {
    }

    override fun getIsCloakEnabled(): Boolean {
        return false
    }

    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
    }

    override fun getOutlineKey(): String {
        return "OutlineKey"
    }

    override fun setOutlineKey(newOutlineKey: String) {
    }

    override fun getIsOutlineEnabled(): Boolean {
        return false
    }

    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
    }

    override fun getAwgConfig(): String {
        return "AwgConfig"
    }

    override fun setAwgConfig(newConfig: String) {
    }

    override fun getIsAmneziaWGEnabled(): Boolean {
        return false
    }

    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) {
    }
}
