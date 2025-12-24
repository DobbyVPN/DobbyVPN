package com.dobby.feature.main.domain

interface DobbyConfigsRepository :
    DobbyConfigsRepositoryOutline,
    DobbyConfigsRepositoryCloak,
    DobbyConfigsRepositoryAwg {

    // region global configs

    fun getVpnInterface(): VpnInterface

    fun setVpnInterface(vpnInterface: VpnInterface)

    fun getConnectionURL(): String

    fun setConnectionURL(connectionURL: String)

    fun getConnectionConfig(): String

    fun setConnectionConfig(connectionConfig: String)

    // endregion

    fun couldStart(): Boolean
}

enum class VpnInterface {
    CLOAK_OUTLINE,
    AMNEZIA_WG;

    companion object {
        val DEFAULT_VALUE = CLOAK_OUTLINE
    }
}
