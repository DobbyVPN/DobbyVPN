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
    fun getCloakConfig(): String

    fun setCloakConfig(newConfig: String)

    fun getIsCloakEnabled(): Boolean

    fun setIsCloakEnabled(isCloakEnabled: Boolean)

    fun getCloakLocalPort(): Int

    fun setCloakLocalPort(port: Int)
    // endregion

    // region outline
    fun setServerPortOutline(newConfig: String)

    fun setMethodPasswordOutline(newConfig: String)

    fun getServerPortOutline() : String

    fun getMethodPasswordOutline() : String

    fun getIsOutlineEnabled(): Boolean

    fun setIsOutlineEnabled(isOutlineEnabled: Boolean)

    fun getPrefixOutline(): String

    fun setPrefixOutline(prefix: String)

    // WebSocket transport options
    fun getIsWebsocketEnabled(): Boolean

    fun setIsWebsocketEnabled(enabled: Boolean)

    fun getTcpPathOutline(): String

    fun setTcpPathOutline(tcpPath: String)

    fun getUdpPathOutline(): String

    fun setUdpPathOutline(udpPath: String)
    // endregion

    // region amnezia
    fun getAwgConfig(): String

    fun setAwgConfig(newConfig: String)

    fun getIsAmneziaWGEnabled(): Boolean

    fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean)
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
