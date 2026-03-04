package com.dobby.feature.main.domain.config

import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline

internal class FakeCloakRepo(
    initialCloakConfig: String = "",
    initialCloakEnabled: Boolean = false,
    initialCloakLocalPort: Int = 0,
) : DobbyConfigsRepositoryCloak {
    var cloakConfigValue: String = initialCloakConfig
    var cloakEnabledValue: Boolean = initialCloakEnabled
    var cloakLocalPortValue: Int = initialCloakLocalPort

    override fun getCloakConfig(): String = cloakConfigValue
    override fun setCloakConfig(newConfig: String) { cloakConfigValue = newConfig }
    override fun getIsCloakEnabled(): Boolean = cloakEnabledValue
    override fun setIsCloakEnabled(isCloakEnabled: Boolean) { cloakEnabledValue = isCloakEnabled }
    override fun getCloakLocalPort(): Int = cloakLocalPortValue
    override fun setCloakLocalPort(port: Int) { cloakLocalPortValue = port }
}

internal data class FakeOutlineRepo(
    var methodPassword: String = "",
    var serverPort: String = "",
    var isOutlineEnabled: Boolean = false,
    var prefix: String = "",
    var websocketEnabled: Boolean = false,
    var tcpPath: String = "",
    var udpPath: String = "",
) : DobbyConfigsRepositoryOutline {
    override fun setServerPortOutline(newConfig: String) { serverPort = newConfig }
    override fun setMethodPasswordOutline(newConfig: String) { methodPassword = newConfig }
    override fun getServerPortOutline(): String = serverPort
    override fun getMethodPasswordOutline(): String = methodPassword
    override fun getIsOutlineEnabled(): Boolean = isOutlineEnabled
    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) { this.isOutlineEnabled = isOutlineEnabled }
    override fun getPrefixOutline(): String = prefix
    override fun setPrefixOutline(prefix: String) { this.prefix = prefix }
    override fun getIsWebsocketEnabled(): Boolean = websocketEnabled
    override fun setIsWebsocketEnabled(enabled: Boolean) { websocketEnabled = enabled }
    override fun getTcpPathOutline(): String = tcpPath
    override fun setTcpPathOutline(tcpPath: String) { this.tcpPath = tcpPath }
    override fun getUdpPathOutline(): String = udpPath
    override fun setUdpPathOutline(udpPath: String) { this.udpPath = udpPath }
}
