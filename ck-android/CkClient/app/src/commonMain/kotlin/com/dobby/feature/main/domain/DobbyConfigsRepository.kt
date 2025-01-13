package com.dobby.feature.main.domain

interface DobbyConfigsRepository {

    // region cloak
    fun getCloakConfig(): String

    fun setCloakConfig(newConfig: String)

    fun getIsCloakEnabled(): Boolean

    fun setIsCloakEnabled(isCloakEnabled: Boolean)
    // endregion

    // region outline
    fun getOutlineKey(): String

    fun setOutlineKey(newOutlineKey: String)

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
