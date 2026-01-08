package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryOutline {
    fun setServerPortOutline(newConfig: String)

    fun setMethodPasswordOutline(newConfig: String)

    fun getServerPortOutline(): String

    fun getMethodPasswordOutline(): String

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
}


