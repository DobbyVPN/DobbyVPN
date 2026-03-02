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
class AndroidThinClientBaselineInstrumentedTest {

    @Test
    fun happy_path_connect_traffic_disconnect_contract() = runBlocking {
        val vm = createViewModel()
        val vpn = vm.vpn
        val state = vm.connectionState

        vm.mainViewModel.startVpnService()
        delay(150)
        state.updateStatus(true)
        delay(50)
        vm.mainViewModel.stopVpnService()
        state.tryUpdateVpnStarted(false)
        delay(50)

        assertEquals(1, vpn.startCalls)
        assertEquals(1, vpn.stopCalls)
        assertEquals(0, vpn.activeTunnels)
        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
    }

    @Test
    fun invalid_config_no_false_connected_contract() = runBlocking {
        val vm = createViewModel()

        vm.mainViewModel.onConnectionButtonClicked(
            """
            [Outline]
            Server = "broken.example.org"
            Port = 443
            """.trimIndent()
        )
        delay(250)

        assertEquals(0, vm.vpn.startCalls)
        assertFalse(vm.connectionState.vpnStartedFlow.value)
        assertFalse(vm.connectionState.statusFlow.value)
    }

    @Test
    fun stop_cleanup_no_hanging_tunnels_contract() = runBlocking {
        val vm = createViewModel()

        vm.mainViewModel.startVpnService()
        delay(100)
        vm.mainViewModel.stopVpnService()
        vm.connectionState.tryUpdateVpnStarted(false)
        delay(50)

        assertEquals(0, vm.vpn.activeTunnels)
        assertTrue(vm.configs.methodPasswordOutline.isEmpty())
        assertTrue(vm.configs.serverPortOutline.isEmpty())
        assertFalse(vm.connectionState.statusFlow.value)
    }

    private fun createViewModel(): VmBundle {
        initLogFilePath(InstrumentationRegistry.getInstrumentation().targetContext)

        val logger = Logger(LogsRepository(logEventsChannel = LogEventsChannel()))
        val configs = BaselineFakeConfigs(
            methodPasswordOutline = "chacha20-ietf-poly1305:secret",
            serverPortOutline = "example.org:443",
            isOutlineEnabled = true
        )
        val state = ConnectionStateRepository().also { it.tryUpdateVpnStarted(true) }
        val vpn = BaselineCountingVpnManager()

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
        return VmBundle(vm, configs, state, vpn)
    }
}

private data class VmBundle(
    val mainViewModel: MainViewModel,
    val configs: BaselineFakeConfigs,
    val connectionState: ConnectionStateRepository,
    val vpn: BaselineCountingVpnManager
)

private class BaselineCountingVpnManager : VpnManager {
    var startCalls: Int = 0
    var stopCalls: Int = 0
    var activeTunnels: Int = 0

    override fun start() {
        startCalls++
        activeTunnels++
    }

    override fun stop() {
        stopCalls++
        if (activeTunnels > 0) activeTunnels--
    }
}

private data class BaselineFakeConfigs(
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
