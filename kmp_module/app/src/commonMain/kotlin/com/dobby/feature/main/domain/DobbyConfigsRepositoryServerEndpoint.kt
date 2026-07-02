package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryServerEndpoint {
    fun setServerPort(newConfig: String)

    fun getServerPort(): String

    fun setServerHostname(hostname: String)

    fun getServerHostname(): String
}
