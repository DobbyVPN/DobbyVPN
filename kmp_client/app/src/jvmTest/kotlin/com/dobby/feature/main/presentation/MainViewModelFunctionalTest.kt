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

private data class VmFakeConfigs(
    var vpnInterface: VpnInterface = VpnInterface.CLOAK_OUTLINE,
    var connectionUrl: String = "",
    var connectionConfig: String = "",
    var methodPasswordOutline: String = "",
    var serverPortOutline: String = "",
    var isOutlineEnabled: Boolean = false,
    var prefixOutline: String = "",
    var isWebsocketEnabled: Boolean = false,
    var tcpPathOutline: String = "",
    var udpPathOutline: String = "",
    var cloakConfig: String = "",
    var isCloakEnabled: Boolean = false,
    var cloakLocalPort: Int = 1984,
    var awgConfig: String = "",
    var isAmneziaWGEnabled: Boolean = false,
    var isUserInitStop: Boolean = false,
) : DobbyConfigsRepository {
    override fun getVpnInterface(): VpnInterface = vpnInterface
    override fun setVpnInterface(vpnInterface: VpnInterface) {
        this.vpnInterface = vpnInterface
    }

    override fun getConnectionURL(): String = connectionUrl
    override fun setConnectionURL(connectionURL: String) {
        connectionUrl = connectionURL
    }

    override fun getConnectionConfig(): String = connectionConfig
    override fun setConnectionConfig(connectionConfig: String) {
        this.connectionConfig = connectionConfig
    }

    override fun couldStart(): Boolean = true
    override fun getIsUserInitStop(): Boolean = isUserInitStop
    override fun setIsUserInitStop(isUserInitStop: Boolean) {
        this.isUserInitStop = isUserInitStop
    }

    override fun setServerPortOutline(newConfig: String) {
        serverPortOutline = newConfig
    }
    override fun setMethodPasswordOutline(newConfig: String) {
        methodPasswordOutline = newConfig
    }
    override fun getServerPortOutline(): String = serverPortOutline
    override fun getMethodPasswordOutline(): String = methodPasswordOutline
    override fun getIsOutlineEnabled(): Boolean = isOutlineEnabled
    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        this.isOutlineEnabled = isOutlineEnabled
    }
    override fun getPrefixOutline(): String = prefixOutline
    override fun setPrefixOutline(prefix: String) {
        prefixOutline = prefix
    }
    override fun getIsWebsocketEnabled(): Boolean = isWebsocketEnabled
    override fun setIsWebsocketEnabled(enabled: Boolean) {
        isWebsocketEnabled = enabled
    }
    override fun getTcpPathOutline(): String = tcpPathOutline
    override fun setTcpPathOutline(tcpPath: String) {
        tcpPathOutline = tcpPath
    }
    override fun getUdpPathOutline(): String = udpPathOutline
    override fun setUdpPathOutline(udpPath: String) {
        udpPathOutline = udpPath
    }

    override fun getCloakConfig(): String = cloakConfig
    override fun setCloakConfig(newConfig: String) {
        cloakConfig = newConfig
    }
    override fun getIsCloakEnabled(): Boolean = isCloakEnabled
    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        this.isCloakEnabled = isCloakEnabled
    }
    override fun getCloakLocalPort(): Int = cloakLocalPort
    override fun setCloakLocalPort(port: Int) {
        cloakLocalPort = port
    }

    override fun getAwgConfig(): String = awgConfig
    override fun setAwgConfig(newConfig: String) {
        awgConfig = newConfig
    }
    override fun getIsAmneziaWGEnabled(): Boolean = isAmneziaWGEnabled
    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) {
        this.isAmneziaWGEnabled = isAmneziaWGEnabled
    }
}
