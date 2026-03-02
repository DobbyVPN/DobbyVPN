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

class MainViewModelFunctionalTest {

    @Test
    fun `boundary contract start then stop maps to vpn manager calls`() = runBlocking {
        val configs = VmFakeConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = VmCountingVpnManager()
        val vm = createVm(configs, connectionState, vpn)

        vm.onConnectionButtonClicked(validOutlineConfig())
        delay(200)

        assertTrue(connectionState.vpnStartedFlow.value)
        assertTrue(vpn.startCalls >= 1)

        vm.onConnectionButtonClicked(validOutlineConfig())
        delay(200)

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertTrue(vpn.stopCalls >= 1)
    }

    @Test
    fun `invalid config maps to error path and no vpn start`() = runBlocking {
        val configs = VmFakeConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = VmCountingVpnManager()
        val vm = createVm(configs, connectionState, vpn)

        vm.onConnectionButtonClicked(
            """
            [Outline]
            Server = "example.org"
            Port = 443
            """.trimIndent()
        )
        delay(200)

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertEquals(0, vpn.startCalls)
    }

    @Test
    fun `idempotency repeated toggles do not leave intermediate connected state`() = runBlocking {
        val configs = VmFakeConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = VmCountingVpnManager()
        val vm = createVm(configs, connectionState, vpn)
        val cfg = validOutlineConfig()

        repeat(4) {
            vm.onConnectionButtonClicked(cfg)
            delay(120)
        }

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertTrue(vpn.startCalls >= 2)
        assertTrue(vpn.stopCalls >= 2)
    }

    @Test
    fun `transition guard keeps UI state consistent for rapid stop-start race`() = runBlocking {
        val configs = VmFakeConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = VmCountingVpnManager()
        val vm = createVm(configs, connectionState, vpn)
        val cfg = validOutlineConfig()

        vm.onConnectionButtonClicked(cfg)
        vm.onConnectionButtonClicked(cfg)
        vm.onConnectionButtonClicked(cfg)
        delay(400)

        assertFalse(!connectionState.vpnStartedFlow.value && connectionState.statusFlow.value)
    }

    @Test
    fun `cross impl parity same scenario gives same semantic ui result`() = runBlocking {
        val resultA = runScenarioWith(VmCountingVpnManager())
        val resultB = runScenarioWith(VmCountingVpnManager())

        assertEquals(resultA.finalStarted, resultB.finalStarted)
        assertEquals(resultA.finalConnected, resultB.finalConnected)
    }

    @Test
    fun `non critical UI contract connection url is reflected in ui state`() = runBlocking {
        val configs = VmFakeConfigs(connectionUrl = "initial")
        val connectionState = ConnectionStateRepository()
        val vm = createVm(configs, connectionState, VmCountingVpnManager())
        delay(100)
        assertEquals("initial", vm.uiState.value.connectionURL)

        vm.onConnectionUrlChanged("updated-url")
        delay(100)
        assertEquals("updated-url", vm.uiState.value.connectionURL)
    }

    private suspend fun runScenarioWith(vpnManager: VmCountingVpnManager): VmScenarioResult {
        val configs = VmFakeConfigs()
        val connectionState = ConnectionStateRepository()
        val vm = createVm(configs, connectionState, vpnManager)
        val cfg = validOutlineConfig()

        vm.onConnectionButtonClicked(cfg)
        delay(150)
        vm.onConnectionButtonClicked(cfg)
        delay(150)

        return VmScenarioResult(
            finalStarted = connectionState.vpnStartedFlow.value,
            finalConnected = connectionState.statusFlow.value
        )
    }

    private fun createVm(
        configs: DobbyConfigsRepository,
        connectionStateRepository: ConnectionStateRepository,
        vpnManager: VpnManager
    ): MainViewModel {
        val logger = Logger(LogsRepository(logEventsChannel = LogEventsChannel()))
        return MainViewModel(
            configsRepository = configs,
            connectionStateRepository = connectionStateRepository,
            permissionEventsChannel = PermissionEventsChannel(),
            vpnManager = vpnManager,
            awgManager = object : AwgManager {
                override fun getAwgVersion(): String = "test"
                override fun onAwgConnect() = Unit
                override fun onAwgDisconnect() = Unit
            },
            logger = logger,
            healthCheck = object : HealthCheck {
                override fun shortConnectionCheckUp(): Boolean = true
                override fun fullConnectionCheckUp(): Boolean = true
                override fun checkServerAlive(address: String, port: Int): Boolean = true
                override fun getTimeToWakeUp(): Int = 60
            }
        )
    }

    private fun validOutlineConfig(): String = """
        [Outline]
        Server = "example.org"
        Port = 443
        Method = "chacha20-ietf-poly1305"
        Password = "secret-pass"
    """.trimIndent()
}

private data class VmScenarioResult(
    val finalStarted: Boolean,
    val finalConnected: Boolean
)

private class VmCountingVpnManager : VpnManager {
    var startCalls: Int = 0
    var stopCalls: Int = 0
    override fun start() {
        startCalls++
    }

    override fun stop() {
        stopCalls++
    }
}

private class VmFakeConfigs(
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
