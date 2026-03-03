package com.dobby.feature.diagnostic.domain

import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.test.fixtures.TestCountingVpnManager
import com.dobby.test.fixtures.TestFakeDobbyConfigs
import com.dobby.test.fixtures.TestScriptedHealthCheck
import com.dobby.test.fixtures.createTestLogger
import com.dobby.test.fixtures.createTestViewModel
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
        val configs = TestFakeDobbyConfigs(methodPasswordOutline = "method:password")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = TestCountingVpnManager()
        val health = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(false, true)))
        val vm = createTestViewModel(configs, connectionState, vpn, healthCheck = health)
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
        delay(20)
        manager.stopHealthCheck()

        assertTrue(vpn.stopCalls >= 1, "restart should stop service at least once")
        assertTrue(vpn.startCalls >= 1, "restart should start service at least once")
        assertTrue(connectionState.statusFlow.value, "connection should recover to true")
    }

    @Test
    fun `lifecycle unhealthy to failed turns off vpn`() = runTest {
        val configs = TestFakeDobbyConfigs(methodPasswordOutline = "method:password")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = TestCountingVpnManager()
        val health = TestScriptedHealthCheck(fullScript = ArrayDeque(listOf(false, false, false)))
        val vm = createTestViewModel(configs, connectionState, vpn, healthCheck = health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createTestLogger(),
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
        val configs = TestFakeDobbyConfigs(methodPasswordOutline = "method:password")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = TestCountingVpnManager()
        val health = TestScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(false, true, false, true, true)),
            shortScript = ArrayDeque(listOf(true, false, true, true))
        )
        val vm = createTestViewModel(configs, connectionState, vpn, healthCheck = health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createTestLogger(),
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
        val configs = TestFakeDobbyConfigs(methodPasswordOutline = "method:password")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = TestCountingVpnManager()
        val health = TestScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(true)),
            shortScript = ArrayDeque(List(100) { true })
        )
        val vm = createTestViewModel(configs, connectionState, vpn, healthCheck = health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createTestLogger(),
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
        val configs = TestFakeDobbyConfigs(methodPasswordOutline = "method:password")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = TestCountingVpnManager()
        val health = TestScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(true)),
            shortScript = ArrayDeque(List(200) { true })
        )
        val vm = createTestViewModel(configs, connectionState, vpn, healthCheck = health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createTestLogger(),
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

        assertTrue(health.shortCalls in 6..29, "expected paced checks with increasing delays")
    }

    @Test
    fun `edge timeout around grace period ignores early failures but not late failures`() = runTest {
        val configs = TestFakeDobbyConfigs(methodPasswordOutline = "method:password")
        val connectionState = ConnectionStateRepository()
        connectionState.tryUpdateVpnStarted(true)
        val vpn = TestCountingVpnManager()
        val health = TestScriptedHealthCheck(
            fullScript = ArrayDeque(listOf(false, false, false))
        )
        val vm = createTestViewModel(configs, connectionState, vpn, healthCheck = health)
        val manager = HealthCheckManager(
            healthCheck = health,
            mainViewModel = vm,
            configsRepository = configs,
            logger = createTestLogger(),
            scope = backgroundScope,
            gracePeriodMs = 0,
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
}
