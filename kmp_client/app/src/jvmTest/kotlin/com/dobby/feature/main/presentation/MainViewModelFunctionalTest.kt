package com.dobby.feature.main.presentation

import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.test.fixtures.TestCountingVpnManager
import com.dobby.test.fixtures.TestFakeDobbyConfigs
import com.dobby.test.fixtures.createTestViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class MainViewModelFunctionalTest {

    @Test
    fun `boundary contract start then stop maps to vpn manager calls`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, connectionState, vpn)

        vm.onConnectionButtonClicked(validOutlineConfig())
        advanceUntilIdle()

        assertTrue(connectionState.vpnStartedFlow.value)
        assertTrue(vpn.startCalls >= 1)

        vm.onConnectionButtonClicked(validOutlineConfig())
        advanceUntilIdle()

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertTrue(vpn.stopCalls >= 1)
    }

    @Test
    fun `invalid config maps to error path and no vpn start`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, connectionState, vpn)

        vm.onConnectionButtonClicked(
            """
            [Outline]
            Server = "example.org"
            Port = 443
            """.trimIndent()
        )
        advanceUntilIdle()

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertEquals(0, vpn.startCalls)
    }

    @Test
    fun `idempotency repeated toggles do not leave intermediate connected state`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, connectionState, vpn)
        val cfg = validOutlineConfig()

        repeat(4) {
            vm.onConnectionButtonClicked(cfg)
            advanceUntilIdle()
        }

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertTrue(vpn.startCalls >= 2)
        assertTrue(vpn.stopCalls >= 2)
    }

    @Test
    fun `transition guard keeps UI state consistent for rapid stop-start race`() = runTest {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, connectionState, vpn)
        val cfg = validOutlineConfig()

        vm.onConnectionButtonClicked(cfg)
        vm.onConnectionButtonClicked(cfg)
        vm.onConnectionButtonClicked(cfg)
        advanceUntilIdle()

        assertFalse(!connectionState.vpnStartedFlow.value && connectionState.statusFlow.value)
    }

    @Test
    fun `cross impl parity same scenario gives same semantic ui result`() = runTest {
        val resultA = runScenarioWith(TestCountingVpnManager())
        val resultB = runScenarioWith(TestCountingVpnManager())

        assertEquals(resultA.finalStarted, resultB.finalStarted)
        assertEquals(resultA.finalConnected, resultB.finalConnected)
    }

    @Test
    fun `non critical UI contract connection url is reflected in ui state`() = runTest {
        val configs = TestFakeDobbyConfigs(connectionUrl = "initial")
        val connectionState = ConnectionStateRepository()
        val vm = createTestViewModel(configs, connectionState, TestCountingVpnManager())
        advanceUntilIdle()
        assertEquals("initial", vm.uiState.value.connectionURL)

        vm.onConnectionUrlChanged("updated-url")
        advanceUntilIdle()
        assertEquals("updated-url", vm.uiState.value.connectionURL)
    }

    private suspend fun runScenarioWith(vpnManager: TestCountingVpnManager): ScenarioResult {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vm = createTestViewModel(configs, connectionState, vpnManager)
        val cfg = validOutlineConfig()

        vm.onConnectionButtonClicked(cfg)
        vm.onConnectionButtonClicked(cfg)

        return ScenarioResult(
            finalStarted = connectionState.vpnStartedFlow.value,
            finalConnected = connectionState.statusFlow.value
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

private data class ScenarioResult(
    val finalStarted: Boolean,
    val finalConnected: Boolean
)
