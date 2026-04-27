package com.dobby.cli

import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.AwgManagerImpl
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.grpc.RestartableAwgGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableCloakGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableGeoroutingGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableHealthCheckGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableLoggerGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableOutlineGrpcLibrary
import kotlinx.coroutines.runBlocking
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path

class CliClient {
    fun connect(configPath: String) {
        val path = Path.of(configPath)
        println("Reading config")
        val config = try {
            String(Files.readAllBytes(path))
        } catch (e: IOException) {
            println("Failed to read config: $e")
            return
        }

        println("Building dependencies")
        println("logEventsChannel")
        val logEventsChannel = LogEventsChannel()
        println("logsRepository")
        val logsRepository = LogsRepository(logEventsChannel = logEventsChannel)
        println("logger")
        val logger = Logger(logsRepository)

        println("libraries")
        val healthCheckLibrary = RestartableHealthCheckGrpcLibrary(logger)
        val awgLibrary = RestartableAwgGrpcLibrary(logger)
        val outlineLibrary = RestartableOutlineGrpcLibrary(logger)
        val cloakLibrary = RestartableCloakGrpcLibrary(logger)
        val loggerLibrary = RestartableLoggerGrpcLibrary(logger)
        val georoutingLibrary = RestartableGeoroutingGrpcLibrary(logger)

        println("configsRepository")
        val configsRepository = DobbyConfigsRepositoryImpl(healthCheckLibrary = healthCheckLibrary)
        println("connectionStateRepository")
        val connectionStateRepository = ConnectionStateRepository()
        println("permissionEventsChannel")
        val permissionEventsChannel = PermissionEventsChannel()
        println("connectionState")
        val connectionState = ConnectionStateRepository()
        println("dobbyVpnService")
        val dobbyVpnService = DobbyVpnService(
            dobbyConfigsRepository = configsRepository,
            logger = logger,
            awgLibrary = awgLibrary,
            outlineLibrary = outlineLibrary,
            cloakLibrary = cloakLibrary,
            loggerLibrary = loggerLibrary,
            georoutingLibrary = georoutingLibrary,
            connectionState = connectionState,
        )
        println("vpnManager")
        val vpnManager = VpnManagerImpl(dobbyVpnService)
        println("mainViewModel")
        val mainViewModel = CliMainViewModel(
            configsRepository = configsRepository,
            connectionStateRepository = connectionStateRepository,
            permissionEventsChannel = permissionEventsChannel,
            vpnManager = vpnManager,
            logger = logger,
            healthCheck = HealthCheckImpl(logger, healthCheckLibrary),
        )

        println("Connecting")
        mainViewModel.onConnectionButtonClicked(config)

        println("Running log print")
        runBlocking {
            logEventsChannel.logEvents.collect { line ->
                println(line)
            }
        }
    }

    fun disconnect(string: String) {

    }
}
