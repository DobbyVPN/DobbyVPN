package com.dobby.feature.main.domain

import kotlinx.serialization.Serializable

interface DobbyConfigsRepository :
    DobbyConfigsRepositoryOutline,
    DobbyConfigsRepositoryCloak,
    DobbyConfigsRepositoryXray,
    DobbyConfigsRepositoryVpn {

    fun getConnectionURL(): String

    fun setConnectionURL(connectionURL: String)

    fun getConnectionConfig(): String

    fun setConnectionConfig(connectionConfig: String)

    fun getConnectionProfiles(): String

    fun setConnectionProfiles(connectionProfiles: String)

    fun getActiveConnectionProfileIndex(): Int

    fun setActiveConnectionProfileIndex(index: Int)

    fun couldStart(): Boolean

    fun getTelemetryEndpoint(): String

    fun setTelemetryEndpoint(endpoint: String)

    fun getTelemetryApiToken(): String

    fun setTelemetryApiToken(token: String)

    fun getTelemetryAttributes(): String

    fun setTelemetryAttributes(config: String)

    fun getGeoRoutingConf(): String

    fun setGeoRoutingConf(geoRoutingConf: String)
}

@Serializable
enum class VpnInterface {
    CLOAK_OUTLINE,
    XRAY,
    NONE;

    companion object {
        val DEFAULT_VALUE = CLOAK_OUTLINE
    }
}
