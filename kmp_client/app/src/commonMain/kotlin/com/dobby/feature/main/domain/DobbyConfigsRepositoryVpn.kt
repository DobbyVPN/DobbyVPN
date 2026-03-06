package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryVpn {
    fun getVpnInterface(): VpnInterface

    fun setVpnInterface(vpnInterface: VpnInterface)
}


