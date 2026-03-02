package com.dobby.feature.main.presentation

import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.VpnManager
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MainViewModelStateFunctionalTest {

    @Test
    fun `state is restored on viewmodel recreation`() = runBlocking {
        val configs = FakeConfigsRepository(connectionUrl = "https://cfg.example")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateStatus(true)
        connectionState.tryUpdateVpnStarted(true)

        val vm1 = createViewModel(configs, connectionState)
        delay(150)
        assertEquals("https://cfg.example", vm1.uiState.value.connectionURL)
        assertTrue(vm1.uiState.value.isConnected)
        assertTrue(vm1.uiState.value.isVpnStarted)

        val vm2 = createViewModel(configs, connectionState)
        delay(150)
        assertEquals("https://cfg.example", vm2.uiState.value.connectionURL)
        assertTrue(vm2.uiState.value.isConnected)
        assertTrue(vm2.uiState.value.isVpnStarted)
    }

    @Test
    fun `ui state tracks repository status updates`() = runBlocking {
        val configs = FakeConfigsRepository(connectionUrl = "inline-toml")
        val connectionState = ConnectionStateRepository()
        val vm = createViewModel(configs, connectionState)
        delay(100)

        assertFalse(vm.uiState.value.isConnected)
        assertFalse(vm.uiState.value.isVpnStarted)

        connectionState.updateStatus(true)
        connectionState.updateVpnStarted(true)
        delay(100)

        assertTrue(vm.uiState.value.isConnected)
        assertTrue(vm.uiState.value.isVpnStarted)
    }

    private fun createViewModel(
        configs: DobbyConfigsRepository,
        connectionState: ConnectionStateRepository
    ): MainViewModel {
        val logger = Logger(LogsRepository(logEventsChannel = LogEventsChannel()))
        return MainViewModel(
            configsRepository = configs,
            connectionStateRepository = connectionState,
            permissionEventsChannel = PermissionEventsChannel(),
            vpnManager = FakeVpnManager(),
            awgManager = FakeAwgManager(),
            logger = logger,
            healthCheck = FakeHealthCheck()
        )
    }
}

private class FakeHealthCheck : HealthCheck {
    override fun shortConnectionCheckUp(): Boolean = true
    override fun fullConnectionCheckUp(): Boolean = true
    override fun checkServerAlive(address: String, port: Int): Boolean = true
    override fun getTimeToWakeUp(): Int = 1
}

private class FakeVpnManager : VpnManager {
    override fun start() = Unit
    override fun stop() = Unit
}

private class FakeAwgManager : AwgManager {
    override fun getAwgVersion(): String = "test"
    override fun onAwgConnect() = Unit
    override fun onAwgDisconnect() = Unit
}

private class FakeConfigsRepository(
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

    override fun getVpnInterface(): VpnInterface = _vpnInterface
    override fun setVpnInterface(vpnInterface: VpnInterface) {
        this._vpnInterface = vpnInterface
    }

    override fun getConnectionURL(): String = _connectionUrl
    override fun setConnectionURL(connectionURL: String) {
        _connectionUrl = connectionURL
    }

    override fun getConnectionConfig(): String = _connectionConfig
    override fun setConnectionConfig(connectionConfig: String) {
        this._connectionConfig = connectionConfig
    }

    override fun couldStart(): Boolean = true
    override fun getIsUserInitStop(): Boolean = _isUserInitStop
    override fun setIsUserInitStop(isUserInitStop: Boolean) {
        this._isUserInitStop = isUserInitStop
    }

    override fun setServerPortOutline(newConfig: String) {
        _serverPortOutline = newConfig
    }
    override fun setMethodPasswordOutline(newConfig: String) {
        _methodPasswordOutline = newConfig
    }
    override fun getServerPortOutline(): String = _serverPortOutline
    override fun getMethodPasswordOutline(): String = _methodPasswordOutline
    override fun getIsOutlineEnabled(): Boolean = _isOutlineEnabled
    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        this._isOutlineEnabled = isOutlineEnabled
    }
    override fun getPrefixOutline(): String = _prefixOutline
    override fun setPrefixOutline(prefix: String) {
        _prefixOutline = prefix
    }
    override fun getIsWebsocketEnabled(): Boolean = _isWebsocketEnabled
    override fun setIsWebsocketEnabled(enabled: Boolean) {
        _isWebsocketEnabled = enabled
    }
    override fun getTcpPathOutline(): String = _tcpPathOutline
    override fun setTcpPathOutline(tcpPath: String) {
        _tcpPathOutline = tcpPath
    }
    override fun getUdpPathOutline(): String = _udpPathOutline
    override fun setUdpPathOutline(udpPath: String) {
        _udpPathOutline = udpPath
    }

    override fun getCloakConfig(): String = _cloakConfig
    override fun setCloakConfig(newConfig: String) {
        _cloakConfig = newConfig
    }
    override fun getIsCloakEnabled(): Boolean = _isCloakEnabled
    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        this._isCloakEnabled = isCloakEnabled
    }
    override fun getCloakLocalPort(): Int = _cloakLocalPort
    override fun setCloakLocalPort(port: Int) {
        _cloakLocalPort = port
    }

    override fun getAwgConfig(): String = _awgConfig
    override fun setAwgConfig(newConfig: String) {
        _awgConfig = newConfig
    }
    override fun getIsAmneziaWGEnabled(): Boolean = _isAmneziaWGEnabled
    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) {
        this._isAmneziaWGEnabled = isAmneziaWGEnabled
    }
}
