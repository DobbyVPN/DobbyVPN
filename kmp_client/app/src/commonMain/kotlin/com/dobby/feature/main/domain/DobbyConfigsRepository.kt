package com.dobby.feature.main.domain

interface DobbyConfigsRepository {

    // region global configs

    fun getVpnInterface(): VpnInterface

    fun setVpnInterface(vpnInterface: VpnInterface)

    fun getConnectionURL(): String

    fun setConnectionURL(connectionURL: String)

    fun getConnectionConfig(): String

    fun setConnectionConfig(connectionConfig: String)

    // endregion

    // region cloak
    fun getIsCloakEnabled(): Boolean

    fun setIsCloakEnabled(isCloakEnabled: Boolean)
    // endregion

    // region outline
    fun getIsOutlineEnabled(): Boolean

    fun setIsOutlineEnabled(isOutlineEnabled: Boolean)
    // endregion

    // region amnezia
    fun getAwgConfig(): String

    fun setAwgConfig(newConfig: String)

    fun getIsAmneziaWGEnabled(): Boolean

    fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean)
    // endregion
}

enum class VpnInterface {
    CLOAK_OUTLINE,
    AMNEZIA_WG;

    companion object {
        val DEFAULT_VALUE = CLOAK_OUTLINE
    }
}
