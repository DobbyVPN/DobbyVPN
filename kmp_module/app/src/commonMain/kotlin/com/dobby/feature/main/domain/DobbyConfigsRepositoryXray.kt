package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryXray {
    fun getXrayConfig(): String

    fun setXrayConfig(config: String)

    fun getIsXrayEnabled(): Boolean

    fun setIsXrayEnabled(isXrayEnabled: Boolean)

    fun setServerPort(newConfig: String)
    fun getServerPort(): String
}
