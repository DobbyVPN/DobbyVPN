package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryCloak {
    fun getCloakConfig(): String

    fun setCloakConfig(newConfig: String)

    fun getIsCloakEnabled(): Boolean

    fun setIsCloakEnabled(isCloakEnabled: Boolean)

    fun getCloakLocalPort(): Int

    fun setCloakLocalPort(port: Int)
}


