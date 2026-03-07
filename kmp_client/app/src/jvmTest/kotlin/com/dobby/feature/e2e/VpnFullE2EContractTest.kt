package com.dobby.feature.e2e

import com.dobby.feature.diagnostic.domain.HealthCheckManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.test.fixtures.TestCountingVpnManager
import com.dobby.test.fixtures.TestFakeDobbyConfigs
import com.dobby.test.fixtures.TestScriptedHealthCheck
import com.dobby.test.fixtures.createTestLogger
import com.dobby.test.fixtures.createTestViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.milliseconds

@OptIn(ExperimentalCoroutinesApi::class)
class VpnFullE2EContractTest {

    @Test
    fun `happy path end-to-end app start connect traffic disconnect`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        advanceUntilIdle()
        state.updateStatus(true)
        advanceUntilIdle()
        vm.onConnectionButtonClicked(validConfigA())
        advanceUntilIdle()

        assertTrue(vpn.startCalls >= 1)
        assertTrue(vpn.stopCalls >= 1)
        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
        assertEquals(0, vpn.activeTunnels)
    }

    @Test
    fun `network down up end-to-end reconnect flow and recovery`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        state.tryUpdateVpnStarted(true)
        val vpn = TestCountingVpnManager()
        val health = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(false, true)))
        val vm = createTestViewModel(configs, state, vpn, healthCheck = health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createTestLogger(),
            scope = backgroundScope,
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
    fun `invalid config end-to-end reports disconnected without false connected`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck())

        vm.onConnectionButtonClicked(
            """
            [Outline]
            Server = "example.org"
            Port = 443
            """.trimIndent()
        )
        advanceUntilIdle()

        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
        assertEquals(0, vpn.startCalls)
    }

    @Test
    fun `stop cleanup end-to-end has no active tunnels or hanging connected state`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        advanceUntilIdle()
        vm.onConnectionButtonClicked(validConfigA())
        advanceUntilIdle()

        assertEquals(0, vpn.activeTunnels)
        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
    }

    @Test
    fun `server profile switch in ui works without app restart`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        advanceUntilIdle()
        vm.onConnectionButtonClicked(validConfigA()) // stop
        advanceUntilIdle()
        vm.onConnectionButtonClicked(validConfigB())
        advanceUntilIdle()

        assertTrue(vpn.startCalls >= 2)
        assertEquals("second.example.org:8443", configs.serverPortOutlineValue)
    }

    @Test
    fun `background foreground transition keeps vpn state consistent`() = runTest {
        val configs = TestFakeDobbyConfigs(connectionUrl = validConfigA())
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm1 = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm1.onConnectionButtonClicked(validConfigA())
        advanceUntilIdle()
        state.updateStatus(true)
        advanceUntilIdle()

        val vm2 = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))
        advanceUntilIdle()

        assertTrue(vm2.uiState.value.isVpnStarted)
        assertTrue(vm2.uiState.value.isConnected)
    }

    @Test
    fun `cold restart recovery keeps state consistent with actual vpn`() = runTest {
        val configs = TestFakeDobbyConfigs(connectionUrl = validConfigA())
        val persistentState = ConnectionStateRepository()
        persistentState.tryUpdateVpnStarted(true)
        persistentState.tryUpdateStatus(true)
        val vpn = TestCountingVpnManager(activeTunnels = 1)

        val vm = createTestViewModel(configs, persistentState, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))
        advanceUntilIdle()

        assertTrue(vm.uiState.value.isVpnStarted)
        assertTrue(vm.uiState.value.isConnected)
        assertEquals(1, vpn.activeTunnels)
    }

    @Test
    fun `long session stability smoke has no state leak on repeated health updates`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        vm.onConnectionButtonClicked(validConfigA())
        advanceUntilIdle()

        repeat(300) { idx ->
            state.updateStatus(idx % 2 == 0)
        }
        advanceUntilIdle()

        assertTrue(state.vpnStartedFlow.value)
        assertFalse(!state.vpnStartedFlow.value && state.statusFlow.value)
    }

    @Test
    fun `ui resilience under transient errors does not freeze state machine`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(true))))

        repeat(5) {
            vm.onConnectionButtonClicked(
                """
                [Outline]
                Server = "broken.example.org"
                Port = 443
                """.trimIndent()
            )
            advanceUntilIdle()
            vm.onConnectionButtonClicked(validConfigA())
            advanceUntilIdle()
            vm.onConnectionButtonClicked(validConfigA())
            advanceUntilIdle()
        }

        vm.onConnectionUrlChanged("stable-url")
        advanceUntilIdle()
        assertEquals("stable-url", vm.uiState.value.connectionURL)
        assertFalse(!state.vpnStartedFlow.value && state.statusFlow.value)
    }

    @Test
    fun `secondary flows diagnostics and settings do not affect core connect path`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val state = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, state, vpn, healthCheck = TestScriptedHealthCheck())

        vm.onConnectionUrlChanged("secondary-flow-url")
        advanceUntilIdle()

        assertEquals("secondary-flow-url", vm.uiState.value.connectionURL)
        assertFalse(state.vpnStartedFlow.value)
        assertFalse(state.statusFlow.value)
        assertEquals(0, vpn.startCalls)
        assertEquals(0, vpn.stopCalls)
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
