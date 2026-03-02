package com.dobby.feature.e2e

import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.diagnostic.domain.HealthCheckManager
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.presentation.MainViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.milliseconds

class VpnFullE2EContractTest {

    @Test
    fun `happy path end-to-end app start connect traffic disconnect`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm = createVm(configs, state, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        delay(250)
        state.updateStatus(true) // emulate traffic flowing after connect
        delay(50)
        vm.onConnectionButtonClicked(validConfigA())
        delay(250)

        assertTrue(vpn.startCalls >= 1)
        assertTrue(vpn.stopCalls >= 1)
        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
        assertEquals(0, vpn.activeTunnels)
    }

    @Test
    fun `network down up end-to-end reconnect flow and recovery`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        state.tryUpdateVpnStarted(true)
        val vpn = E2eCountingVpnManager()
        val health = E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(false, true)))
        val vm = createVm(configs, state, vpn, health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createLogger(),
            gracePeriodMs = 0,
            restartDelayMs = 1,
            postRestartDelay = 1.milliseconds,
            shortDelay = 1.milliseconds,
            mediumDelay = 2.milliseconds,
            longDelay = 3.milliseconds
        )

        manager.startHealthCheck("localhost", 8080)
        delay(40)
        manager.stopHealthCheck()

        assertTrue(vpn.stopCalls >= 1)
        assertTrue(vpn.startCalls >= 1)
        assertTrue(state.statusFlow.value)
    }

    @Test
    fun `invalid config end-to-end reports disconnected without false connected`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm = createVm(configs, state, vpn, E2eScriptedHealthCheck())

        vm.onConnectionButtonClicked(
            """
            [Outline]
            Server = "example.org"
            Port = 443
            """.trimIndent()
        )
        delay(250)

        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
        assertEquals(0, vpn.startCalls)
    }

    @Test
    fun `stop cleanup end-to-end has no active tunnels or hanging connected state`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm = createVm(configs, state, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        delay(250)
        vm.onConnectionButtonClicked(validConfigA())
        delay(250)

        assertEquals(0, vpn.activeTunnels)
        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
    }

    @Test
    fun `server profile switch in ui works without app restart`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm = createVm(configs, state, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        delay(200)
        vm.onConnectionButtonClicked(validConfigA()) // stop
        delay(200)
        vm.onConnectionButtonClicked(validConfigB())
        delay(200)

        assertTrue(vpn.startCalls >= 2)
        assertEquals("second.example.org:8443", configs.serverPortOutline)
    }

    @Test
    fun `background foreground transition keeps vpn state consistent`() = runBlocking {
        val configs = E2eFakeConfigs(connectionUrl = validConfigA())
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm1 = createVm(configs, state, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm1.onConnectionButtonClicked(validConfigA())
        delay(200)
        state.updateStatus(true)
        delay(100)

        val vm2 = createVm(configs, state, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))
        delay(150)

        assertTrue(vm2.uiState.value.isVpnStarted)
        assertTrue(vm2.uiState.value.isConnected)
    }

    @Test
    fun `cold restart recovery keeps state consistent with actual vpn`() = runBlocking {
        val configs = E2eFakeConfigs(connectionUrl = validConfigA())
        val persistentState = ConnectionStateRepository()
        persistentState.tryUpdateVpnStarted(true)
        persistentState.tryUpdateStatus(true)
        val vpn = E2eCountingVpnManager(activeTunnels = 1)

        val vm = createVm(configs, persistentState, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))
        delay(150)

        assertTrue(vm.uiState.value.isVpnStarted)
        assertTrue(vm.uiState.value.isConnected)
        assertEquals(1, vpn.activeTunnels)
    }

    @Test
    fun `long session stability smoke has no state leak on repeated health updates`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm = createVm(configs, state, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        delay(200)

        repeat(300) { idx ->
            state.updateStatus(idx % 2 == 0)
        }
        delay(100)

        assertTrue(state.vpnStartedFlow.value)
        assertFalse(!state.vpnStartedFlow.value && state.statusFlow.value)
    }

    @Test
    fun `ui resilience under transient errors does not freeze state machine`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm = createVm(configs, state, vpn, E2eScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        repeat(5) {
            vm.onConnectionButtonClicked(
                """
                [Outline]
                Server = "broken.example.org"
                Port = 443
                """.trimIndent()
            )
            delay(120)
            vm.onConnectionButtonClicked(validConfigA())
            delay(120)
            vm.onConnectionButtonClicked(validConfigA())
            delay(120)
        }

        vm.onConnectionUrlChanged("stable-url")
        delay(80)
        assertEquals("stable-url", vm.uiState.value.connectionURL)
        assertFalse(!state.vpnStartedFlow.value && state.statusFlow.value)
    }

    @Test
    fun `secondary flows diagnostics and settings do not affect core connect path`() = runBlocking {
        val configs = E2eFakeConfigs()
        val state = ConnectionStateRepository()
        val vpn = E2eCountingVpnManager()
        val vm = createVm(configs, state, vpn, E2eScriptedHealthCheck())

        vm.onConnectionUrlChanged("secondary-flow-url")
        delay(80)

        assertEquals("secondary-flow-url", vm.uiState.value.connectionURL)
        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
        assertEquals(0, vpn.startCalls)
        assertEquals(0, vpn.stopCalls)
    }

    private fun createVm(
        configs: DobbyConfigsRepository,
        state: ConnectionStateRepository,
        vpn: VpnManager,
        health: HealthCheck
    ): MainViewModel {
        return MainViewModel(
            configsRepository = configs,
            connectionStateRepository = state,
            permissionEventsChannel = PermissionEventsChannel(),
            vpnManager = vpn,
            awgManager = object : AwgManager {
                override fun getAwgVersion(): String = "test"
                override fun onAwgConnect() = Unit
                override fun onAwgDisconnect() = Unit
            },
            logger = createLogger(),
            healthCheck = health
        )
    }

    private fun validConfigA(): String = """
        [Outline]
        Server = "first.example.org"
        Port = 443
        Method = "chacha20-ietf-poly1305"
        Password = "secret-pass-1"
    """.trimIndent()

    private fun validConfigB(): String = """
        [Outline]
        Server = "second.example.org"
        Port = 8443
        Method = "chacha20-ietf-poly1305"
        Password = "secret-pass-2"
    """.trimIndent()
}

private fun createLogger(): Logger = Logger(LogsRepository(logEventsChannel = LogEventsChannel()))

private class E2eScriptedHealthCheck(
    private val wakeup: Int = 0,
    private val fullScript: ArrayDeque<Boolean> = ArrayDeque(),
    private val shortScript: ArrayDeque<Boolean> = ArrayDeque()
) : HealthCheck {
    override fun shortConnectionCheckUp(): Boolean = shortScript.removeFirstOrNull() ?: true
    override fun fullConnectionCheckUp(): Boolean = fullScript.removeFirstOrNull() ?: false
    override fun checkServerAlive(address: String, port: Int): Boolean = true
    override fun getTimeToWakeUp(): Int = wakeup
}

private class E2eCountingVpnManager(
    var startCalls: Int = 0,
    var stopCalls: Int = 0,
    var activeTunnels: Int = 0
) : VpnManager {
    override fun start() {
        startCalls++
        activeTunnels++
    }

    override fun stop() {
        stopCalls++
        if (activeTunnels > 0) activeTunnels--
    }
}

private data class E2eFakeConfigs(
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
