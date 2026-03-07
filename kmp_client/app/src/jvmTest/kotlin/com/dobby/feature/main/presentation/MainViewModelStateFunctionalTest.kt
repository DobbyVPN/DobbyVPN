package com.dobby.feature.main.presentation

import com.dobby.feature.main.domain.ConnectionStateRepository
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
class MainViewModelStateFunctionalTest {

    @Test
    fun `state is restored on viewmodel recreation`() = runTest {
        val configs = TestFakeDobbyConfigs(connectionUrl = "https://cfg.example")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateStatus(true)
        connectionState.tryUpdateVpnStarted(true)

        val vm1 = createTestViewModel(configs, connectionState)
        advanceUntilIdle()
        assertEquals("https://cfg.example", vm1.uiState.value.connectionURL)
        assertTrue(vm1.uiState.value.isConnected)
        assertTrue(vm1.uiState.value.isVpnStarted)

        val vm2 = createTestViewModel(configs, connectionState)
        advanceUntilIdle()
        assertEquals("https://cfg.example", vm2.uiState.value.connectionURL)
        assertTrue(vm2.uiState.value.isConnected)
        assertTrue(vm2.uiState.value.isVpnStarted)
    }

    @Test
    fun `ui state tracks repository status updates`() = runTest {
        val configs = TestFakeDobbyConfigs(connectionUrl = "inline-toml")
        val connectionState = ConnectionStateRepository()
        val vm = createTestViewModel(configs, connectionState)
        advanceUntilIdle()

        assertFalse(vm.uiState.value.isConnected)
        assertFalse(vm.uiState.value.isVpnStarted)

        connectionState.updateStatus(true)
        connectionState.updateVpnStarted(true)
        advanceUntilIdle()

        assertTrue(vm.uiState.value.isConnected)
        assertTrue(vm.uiState.value.isVpnStarted)
    }
}
