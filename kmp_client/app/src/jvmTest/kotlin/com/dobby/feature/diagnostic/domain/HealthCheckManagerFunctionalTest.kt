package com.dobby.feature.diagnostic.domain

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
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.milliseconds

class HealthCheckManagerFunctionalTest {

    @Test
    fun `lifecycle unhealthy to reconnect to recovered`() = runTest {
        val configs = HcFakeConfigs()
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = CountingVpnManager()
        val health = ScriptedHealthCheck(fullScript = ArrayDeque(listOf(false, true)))
        val vm = createMainViewModel(configs, connectionState, vpn, health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createLogger(),
            scope = backgroundScope,
            gracePeriodMs = 0,
            restartDelayMs = 1,
            postRestartDelay = 1.milliseconds,
            shortDelay = 1.milliseconds,
            mediumDelay = 2.milliseconds,
            longDelay = 3.milliseconds
        )

        manager.startHealthCheck("localhost", 8080)
        delay(20)
        manager.stopHealthCheck()

        assertTrue(vpn.stopCalls >= 1, "restart should stop service at least once")
        assertTrue(vpn.startCalls >= 1, "restart should start service at least once")
        assertTrue(connectionState.statusFlow.value, "connection should recover to true")
    }

    @Test
    fun `lifecycle unhealthy to failed turns off vpn`() = runTest {
        val configs = HcFakeConfigs()
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = CountingVpnManager()
        val health = ScriptedHealthCheck(fullScript = ArrayDeque(listOf(false, false, false)))
        val vm = createMainViewModel(configs, connectionState, vpn, health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createLogger(),
            scope = backgroundScope,
            gracePeriodMs = 0,
            consecutiveFailuresBeforeTurnOff = 2,
            restartDelayMs = 1,
            postRestartDelay = 1.milliseconds,
            shortDelay = 1.milliseconds,
            mediumDelay = 2.milliseconds,
            longDelay = 3.milliseconds
        )

        manager.startHealthCheck("localhost", 8080)
        delay(20)

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertTrue(vpn.stopCalls >= 1)
    }

    @Test
    fun `network flap emits sane false true transitions without stuck state`() = runTest {
        val configs = HcFakeConfigs()
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = CountingVpnManager()
        val health = ScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(false, true, false, true, true)),
            shortScript = ArrayDeque(listOf(true, false, true, true))
        )
        val vm = createMainViewModel(configs, connectionState, vpn, health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createLogger(),
            scope = backgroundScope,
            gracePeriodMs = 10_000,
            restartDelayMs = 1,
            postRestartDelay = 1.milliseconds,
            shortDelay = 1.milliseconds,
            mediumDelay = 2.milliseconds,
            longDelay = 3.milliseconds
        )

        val states = mutableListOf<Boolean>()
        val collector: Job = backgroundScope.launch {
            connectionState.statusFlow.drop(1).collect { states.add(it) }
        }

        manager.startHealthCheck("localhost", 8080)
        delay(20)
        manager.stopHealthCheck()
        collector.cancel()

        assertTrue(states.contains(false))
        assertTrue(states.contains(true))
        assertFalse(connectionState.vpnStartedFlow.value.not() && connectionState.statusFlow.value)
    }

    @Test
    fun `idempotent start while active does not create duplicated loops`() = runTest {
        val configs = HcFakeConfigs()
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = CountingVpnManager()
        val health = ScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(true)),
            shortScript = ArrayDeque(List(100) { true })
        )
        val vm = createMainViewModel(configs, connectionState, vpn, health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createLogger(),
            scope = backgroundScope,
            gracePeriodMs = 0,
            restartDelayMs = 1,
            postRestartDelay = 1.milliseconds,
            shortDelay = 5.milliseconds,
            mediumDelay = 5.milliseconds,
            longDelay = 5.milliseconds
        )

        manager.startHealthCheck("localhost", 8080)
        manager.startHealthCheck("localhost", 8080)
        delay(30)
        manager.stopHealthCheck()

        assertTrue(health.shortCalls <= 8, "second start should not double check loop load")
    }

    @Test
    fun `retry backoff policy changes polling pace over elapsed time`() = runTest {
        val configs = HcFakeConfigs()
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = CountingVpnManager()
        val health = ScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(true)),
            shortScript = ArrayDeque(List(200) { true })
        )
        val vm = createMainViewModel(configs, connectionState, vpn, health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createLogger(),
            scope = backgroundScope,
            shortDelayThreshold = 20.milliseconds,
            mediumDelayThreshold = 45.milliseconds,
            shortDelay = 2.milliseconds,
            mediumDelay = 5.milliseconds,
            longDelay = 10.milliseconds
        )

        manager.startHealthCheck("localhost", 8080)
        delay(65)
        manager.stopHealthCheck()

        assertTrue(health.shortCalls in 8..25, "expected paced checks with increasing delays")
    }

    @Test
    fun `edge timeout around grace period ignores early failures but not late failures`() = runTest {
        val configs = HcFakeConfigs()
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = CountingVpnManager()
        val health = ScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(false, false, false))
        )
        val vm = createMainViewModel(configs, connectionState, vpn, health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createLogger(),
            scope = backgroundScope,
            gracePeriodMs = 8,
            consecutiveFailuresBeforeTurnOff = 2,
            restartDelayMs = 1,
            postRestartDelay = 2.milliseconds,
            shortDelay = 2.milliseconds,
            mediumDelay = 2.milliseconds,
            longDelay = 2.milliseconds
        )

        manager.startHealthCheck("localhost", 8080)
        delay(30)

        assertFalse(connectionState.vpnStartedFlow.value)
        assertTrue(vpn.stopCalls >= 1)
    }

    private fun createMainViewModel(
        configs: DobbyConfigsRepository,
        connectionState: ConnectionStateRepository,
        vpnManager: VpnManager,
        healthCheck: HealthCheck
    ): MainViewModel {
        return MainViewModel(
            configsRepository = configs,
            connectionStateRepository = connectionState,
            permissionEventsChannel = PermissionEventsChannel(),
            vpnManager = vpnManager,
            awgManager = object : AwgManager {
                override fun getAwgVersion(): String = "test"
                override fun onAwgConnect() = Unit
                override fun onAwgDisconnect() = Unit
            },
            logger = createLogger(),
            healthCheck = healthCheck
        )
    }
}

private fun createLogger(): Logger = Logger(LogsRepository(logEventsChannel = LogEventsChannel()))

private class ScriptedHealthCheck(
    private val wakeup: Int = 0,
    val fullScript: ArrayDeque<Boolean> = ArrayDeque(),
    val shortScript: ArrayDeque<Boolean> = ArrayDeque()
) : HealthCheck {
    var shortCalls: Int = 0
    override fun shortConnectionCheckUp(): Boolean {
        shortCalls++
        return shortScript.removeFirstOrNull() ?: true
    }

    override fun fullConnectionCheckUp(): Boolean {
        return fullScript.removeFirstOrNull() ?: false
    }

    override fun checkServerAlive(address: String, port: Int): Boolean = true
    override fun getTimeToWakeUp(): Int = wakeup
}

private class CountingVpnManager : VpnManager {
    var startCalls: Int = 0
    var stopCalls: Int = 0
    override fun start() {
        startCalls++
    }

    override fun stop() {
        stopCalls++
    }
}

private data class HcFakeConfigs(
    var vpnInterface: VpnInterface = VpnInterface.CLOAK_OUTLINE,
    var connectionUrl: String = "",
    var connectionConfig: String = "",
    var methodPasswordOutline: String = "method:password",
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
