package com.dobby.feature.main.presentation

import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.test.fixtures.TestCountingVpnManager
import com.dobby.test.fixtures.TestFakeDobbyConfigs
import com.dobby.test.fixtures.createTestViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MainViewModelFunctionalTest {

    @Test
    fun `boundary contract start then stop maps to vpn manager calls`() = runBlocking {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, connectionState, vpn)

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
        delay(200)

        assertFalse(connectionState.vpnStartedFlow.value)
        assertFalse(connectionState.statusFlow.value)
        assertEquals(0, vpn.startCalls)
    }

    @Test
    fun `idempotency repeated toggles do not leave intermediate connected state`() = runBlocking {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, connectionState, vpn)
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
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vpn = TestCountingVpnManager()
        val vm = createTestViewModel(configs, connectionState, vpn)
        val cfg = validOutlineConfig()

        vm.onConnectionButtonClicked(cfg)
        vm.onConnectionButtonClicked(cfg)
        vm.onConnectionButtonClicked(cfg)
        delay(400)

        assertFalse(!connectionState.vpnStartedFlow.value && connectionState.statusFlow.value)
    }

    @Test
    fun `cross impl parity same scenario gives same semantic ui result`() = runBlocking {
        val resultA = runScenarioWith(TestCountingVpnManager())
        val resultB = runScenarioWith(TestCountingVpnManager())

        assertEquals(resultA.finalStarted, resultB.finalStarted)
        assertEquals(resultA.finalConnected, resultB.finalConnected)
    }

    @Test
    fun `non critical UI contract connection url is reflected in ui state`() = runBlocking {
        val configs = TestFakeDobbyConfigs(connectionUrl = "initial")
        val connectionState = ConnectionStateRepository()
        val vm = createTestViewModel(configs, connectionState, TestCountingVpnManager())
        delay(100)
        assertEquals("initial", vm.uiState.value.connectionURL)

        vm.onConnectionUrlChanged("updated-url")
        delay(100)
        assertEquals("updated-url", vm.uiState.value.connectionURL)
    }

    private suspend fun runScenarioWith(vpnManager: TestCountingVpnManager): ScenarioResult {
        val configs = TestFakeDobbyConfigs()
        val connectionState = ConnectionStateRepository()
        val vm = createTestViewModel(configs, connectionState, vpnManager)
        val cfg = validOutlineConfig()

        vm.onConnectionButtonClicked(cfg)
        delay(150)
        vm.onConnectionButtonClicked(cfg)
        delay(150)

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
