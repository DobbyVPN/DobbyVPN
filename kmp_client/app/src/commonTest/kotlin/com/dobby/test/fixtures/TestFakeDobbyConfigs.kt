package com.dobby.test.fixtures

import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface

class TestFakeDobbyConfigs(
    vpnInterface: VpnInterface = VpnInterface.CLOAK_OUTLINE,
    connectionUrl: String = "",
    connectionConfig: String = "",
    methodPasswordOutline: String = "",
    serverPortOutline: String = "",
    isOutlineEnabled: Boolean = false,
    prefixOutline: String = "",
    isWebsocketEnabled: Boolean = false,
    tcpPathOutline: String = "",
    udpPathOutline: String = "",
    cloakConfig: String = "",
    isCloakEnabled: Boolean = false,
    cloakLocalPort: Int = 1984,
    awgConfig: String = "",
    isAmneziaWGEnabled: Boolean = false,
    isUserInitStop: Boolean = false,
) : DobbyConfigsRepository {
    private var _vpnInterface: VpnInterface = vpnInterface
    private var _connectionUrl: String = connectionUrl
    private var _connectionConfig: String = connectionConfig
    private var _methodPasswordOutline: String = methodPasswordOutline
    private var _serverPortOutline: String = serverPortOutline
    private var _isOutlineEnabled: Boolean = isOutlineEnabled
    private var _prefixOutline: String = prefixOutline
    private var _isWebsocketEnabled: Boolean = isWebsocketEnabled
    private var _tcpPathOutline: String = tcpPathOutline
    private var _udpPathOutline: String = udpPathOutline
    private var _cloakConfig: String = cloakConfig
    private var _isCloakEnabled: Boolean = isCloakEnabled
    private var _cloakLocalPort: Int = cloakLocalPort
    private var _awgConfig: String = awgConfig
    private var _isAmneziaWGEnabled: Boolean = isAmneziaWGEnabled
    private var _isUserInitStop: Boolean = isUserInitStop

    val serverPortOutlineValue: String get() = _serverPortOutline

    override fun getVpnInterface(): VpnInterface = _vpnInterface
    override fun setVpnInterface(vpnInterface: VpnInterface) { _vpnInterface = vpnInterface }

    override fun getConnectionURL(): String = _connectionUrl
    override fun setConnectionURL(connectionURL: String) { _connectionUrl = connectionURL }

    override fun getConnectionConfig(): String = _connectionConfig
    override fun setConnectionConfig(connectionConfig: String) { _connectionConfig = connectionConfig }

    override fun couldStart(): Boolean = true
    override fun getIsUserInitStop(): Boolean = _isUserInitStop
    override fun setIsUserInitStop(isUserInitStop: Boolean) { _isUserInitStop = isUserInitStop }

    override fun setServerPortOutline(newConfig: String) { _serverPortOutline = newConfig }
    override fun setMethodPasswordOutline(newConfig: String) { _methodPasswordOutline = newConfig }
    override fun getServerPortOutline(): String = _serverPortOutline
    override fun getMethodPasswordOutline(): String = _methodPasswordOutline
    override fun getIsOutlineEnabled(): Boolean = _isOutlineEnabled
    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) { _isOutlineEnabled = isOutlineEnabled }
    override fun getPrefixOutline(): String = _prefixOutline
    override fun setPrefixOutline(prefix: String) { _prefixOutline = prefix }
    override fun getIsWebsocketEnabled(): Boolean = _isWebsocketEnabled
    override fun setIsWebsocketEnabled(enabled: Boolean) { _isWebsocketEnabled = enabled }
    override fun getTcpPathOutline(): String = _tcpPathOutline
    override fun setTcpPathOutline(tcpPath: String) { _tcpPathOutline = tcpPath }
    override fun getUdpPathOutline(): String = _udpPathOutline
    override fun setUdpPathOutline(udpPath: String) { _udpPathOutline = udpPath }

    override fun getCloakConfig(): String = _cloakConfig
    override fun setCloakConfig(newConfig: String) { _cloakConfig = newConfig }
    override fun getIsCloakEnabled(): Boolean = _isCloakEnabled
    override fun setIsCloakEnabled(isCloakEnabled: Boolean) { _isCloakEnabled = isCloakEnabled }
    override fun getCloakLocalPort(): Int = _cloakLocalPort
    override fun setCloakLocalPort(port: Int) { _cloakLocalPort = port }

    override fun getAwgConfig(): String = _awgConfig
    override fun setAwgConfig(newConfig: String) { _awgConfig = newConfig }
    override fun getIsAmneziaWGEnabled(): Boolean = _isAmneziaWGEnabled
    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) { _isAmneziaWGEnabled = isAmneziaWGEnabled }
}
