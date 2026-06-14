package com.dobby.feature.main.domain

interface DobbyConfigsRepository :
    DobbyConfigsRepositoryOutline,
    DobbyConfigsRepositoryCloak,
    DobbyConfigsRepositoryAwg,
    DobbyConfigsRepositoryXray,
    DobbyConfigsRepositoryVpn {

    fun getConnectionURL(): String

    fun setConnectionURL(connectionURL: String)

    fun getConnectionConfig(): String

    fun setConnectionConfig(connectionConfig: String)

    fun couldStart(): Boolean

    fun getTelemetryEndpoint(): String

    fun setTelemetryEndpoint(endpoint: String)

    fun getTelemetryAttributes(): String

    fun setTelemetryAttributes(config: String)

    fun getGeoRoutingConf(): String

    fun setGeoRoutingConf(geoRoutingConf: String)
}

enum class VpnInterface {
    CLOAK_OUTLINE,
    AMNEZIA_WG,
    XRAY,
    NONE;

    companion object {
        val DEFAULT_VALUE = CLOAK_OUTLINE
    }
}
