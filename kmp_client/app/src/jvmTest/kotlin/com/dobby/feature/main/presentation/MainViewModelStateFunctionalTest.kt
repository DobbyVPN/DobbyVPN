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

private data class FakeConfigsRepository(
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
