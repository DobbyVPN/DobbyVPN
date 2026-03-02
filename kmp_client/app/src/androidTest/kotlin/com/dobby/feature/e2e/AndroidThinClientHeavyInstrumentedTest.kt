package com.dobby.feature.e2e

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.initLogFilePath
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.presentation.MainViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.Test
import org.junit.runner.RunWith
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@RunWith(AndroidJUnit4::class)
class AndroidThinClientHeavyInstrumentedTest {

    @Test
    fun network_flap_reconnect_and_recovery_contract() = runBlocking {
        val vm = createViewModel()
        val state = vm.connectionState
        val vpn = vm.vpn

        vm.mainViewModel.startVpnService()
        delay(100)
        state.updateStatus(false) // network down
        delay(80)
        vm.mainViewModel.stopVpnService()
        vm.mainViewModel.startVpnService() // reconnect attempt
        delay(100)
        state.updateStatus(true) // network up
        delay(80)

        assertTrue(vpn.startCalls >= 2)
        assertTrue(vpn.stopCalls >= 1)
        assertTrue(state.statusFlow.value)
    }

    @Test
    fun background_foreground_state_consistency_contract() = runBlocking {
        val vm1 = createViewModel()
        vm1.mainViewModel.startVpnService()
        delay(80)
        vm1.connectionState.updateStatus(true)
        delay(60)

        // Simulate foreground return with re-created ViewModel on same repositories.
        val vm2 = createViewModel(
            configs = vm1.configs,
            state = vm1.connectionState,
            vpn = vm1.vpn
        )
        delay(120)

        assertTrue(vm2.mainViewModel.uiState.value.isVpnStarted)
        assertTrue(vm2.mainViewModel.uiState.value.isConnected)
    }

    @Test
    fun cold_restart_recovery_consistent_with_runtime_state_contract() = runBlocking {
        val configs = HeavyFakeConfigs(connectionUrl = "inline")
        val state = ConnectionStateRepository().also {
            it.tryUpdateVpnStarted(true)
            it.tryUpdateStatus(true)
        }
        val vpn = HeavyCountingVpnManager(activeTunnels = 1)
        val vm = createViewModel(configs, state, vpn)
        delay(120)

        assertTrue(vm.mainViewModel.uiState.value.isVpnStarted)
        assertTrue(vm.mainViewModel.uiState.value.isConnected)
        assertEquals(1, vpn.activeTunnels)
    }

    @Test
    fun long_session_stability_smoke_no_state_leaks_contract() = runBlocking {
        val vm = createViewModel()
        vm.mainViewModel.startVpnService()
        delay(80)

        repeat(200) { i ->
            vm.connectionState.updateStatus(i % 3 != 0)
        }
        delay(100)

        assertTrue(vm.connectionState.vpnStartedFlow.value)
        assertFalse(!vm.connectionState.vpnStartedFlow.value && vm.connectionState.statusFlow.value)
    }

    @Test
    fun ui_resilience_under_transient_errors_contract() = runBlocking {
        val vm = createViewModel()
        repeat(4) {
            vm.mainViewModel.onConnectionButtonClicked(
                """
                [Outline]
                Server = "broken.example.org"
                Port = 443
                """.trimIndent()
            )
            delay(100)
            vm.mainViewModel.onConnectionButtonClicked(
                """
                [Outline]
                Server = "example.org"
                Port = 443
                Method = "chacha20-ietf-poly1305"
                Password = "secret"
                """.trimIndent()
            )
            delay(100)
            vm.mainViewModel.stopVpnService()
            vm.connectionState.tryUpdateVpnStarted(false)
            delay(80)
        }
        vm.mainViewModel.onConnectionUrlChanged("stable-after-errors")
        delay(80)
        assertEquals("stable-after-errors", vm.mainViewModel.uiState.value.connectionURL)
    }

    @Test
    fun secondary_flows_do_not_break_core_path_contract() = runBlocking {
        val vm = createViewModel()
        vm.mainViewModel.onConnectionUrlChanged("secondary")
        delay(60)
        vm.mainViewModel.startVpnService()
        delay(80)
        vm.mainViewModel.stopVpnService()
        vm.connectionState.tryUpdateVpnStarted(false)
        delay(60)

        assertEquals("secondary", vm.mainViewModel.uiState.value.connectionURL)
        assertEquals(1, vm.vpn.startCalls)
        assertEquals(1, vm.vpn.stopCalls)
        assertEquals(0, vm.vpn.activeTunnels)
    }

    private fun createViewModel(
        configs: HeavyFakeConfigs = HeavyFakeConfigs(
            methodPasswordOutline = "chacha20-ietf-poly1305:secret",
            serverPortOutline = "example.org:443",
            isOutlineEnabled = true
        ),
        state: ConnectionStateRepository = ConnectionStateRepository().also { it.tryUpdateVpnStarted(true) },
        vpn: HeavyCountingVpnManager = HeavyCountingVpnManager()
    ): HeavyVmBundle {
        initLogFilePath(InstrumentationRegistry.getInstrumentation().targetContext)
        val logger = Logger(LogsRepository(logEventsChannel = LogEventsChannel()))
        val vm = MainViewModel(
            configsRepository = configs,
            connectionStateRepository = state,
            permissionEventsChannel = PermissionEventsChannel(),
            vpnManager = vpn,
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
        return HeavyVmBundle(vm, configs, state, vpn)
    }
}

private data class HeavyVmBundle(
    val mainViewModel: MainViewModel,
    val configs: HeavyFakeConfigs,
    val connectionState: ConnectionStateRepository,
    val vpn: HeavyCountingVpnManager
)

private class HeavyCountingVpnManager(
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

private data class HeavyFakeConfigs(
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
