package com.dobby.feature.main.domain

interface DobbyConfigsRepository :
    DobbyConfigsRepositoryOutline,
    DobbyConfigsRepositoryCloak,
    DobbyConfigsRepositoryAwg,
    DobbyConfigsRepositoryVpn {

    // region global configs
    fun getConnectionURL(): String

    fun setConnectionURL(connectionURL: String)

    fun getConnectionConfig(): String

    fun setConnectionConfig(connectionConfig: String)

    // endregion

    fun couldStart(): Boolean

    fun getIsUserInitStop(): Boolean

    fun setIsUserInitStop(isUserInitStop: Boolean)
}

enum class VpnInterface {
    CLOAK_OUTLINE,
    AMNEZIA_WG;

    companion object {
        val DEFAULT_VALUE = CLOAK_OUTLINE
    }
}
