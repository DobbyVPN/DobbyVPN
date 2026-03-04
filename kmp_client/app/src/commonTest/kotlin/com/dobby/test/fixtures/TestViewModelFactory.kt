package com.dobby.test.fixtures

import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.presentation.MainViewModel

fun createTestLogger(): Logger =
    Logger(LogsRepository(logEventsChannel = LogEventsChannel()))

fun createTestViewModel(
    configs: DobbyConfigsRepository = TestFakeDobbyConfigs(),
    connectionState: ConnectionStateRepository = ConnectionStateRepository(),
    vpnManager: VpnManager = TestCountingVpnManager(),
    awgManager: AwgManager = TestFakeAwgManager(),
    healthCheck: HealthCheck = TestScriptedHealthCheck(),
    logger: Logger = createTestLogger(),
): MainViewModel = MainViewModel(
    configsRepository = configs,
    connectionStateRepository = connectionState,
    permissionEventsChannel = PermissionEventsChannel(),
    vpnManager = vpnManager,
    awgManager = awgManager,
    logger = logger,
    healthCheck = healthCheck,
)
