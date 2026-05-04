package com.dobby.cli

import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.*
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.grpc.*
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import kotlin.system.exitProcess

class CliClient(private val configPath: String) {
    private val connectionStateRepository: ConnectionStateRepository
    private val configsRepository: DobbyConfigsRepository
    private val logger: Logger
    private val logEventsChannel: LogEventsChannel = LogEventsChannel()
    private val mainViewModel: MainViewModel

    init {
        val logsRepository = LogsRepository(logEventsChannel = logEventsChannel)
        logger = Logger(logsRepository)

        val healthCheckLibrary = RestartableHealthCheckGrpcLibrary(logger)
        val awgLibrary = RestartableAwgGrpcLibrary(logger)
        val outlineLibrary = RestartableOutlineGrpcLibrary(logger)
        val cloakLibrary = RestartableCloakGrpcLibrary(logger)
        val loggerLibrary = RestartableLoggerGrpcLibrary(logger)
        val georoutingLibrary = RestartableGeoroutingGrpcLibrary(logger)

        configsRepository = DobbyConfigsRepositoryImpl(healthCheckLibrary = healthCheckLibrary)
        connectionStateRepository = ConnectionStateRepository()
        val permissionEventsChannel = PermissionEventsChannel()
        val dobbyVpnService = DobbyVpnService(
            dobbyConfigsRepository = configsRepository,
            logger = logger,
            awgLibrary = awgLibrary,
            outlineLibrary = outlineLibrary,
            cloakLibrary = cloakLibrary,
            loggerLibrary = loggerLibrary,
            georoutingLibrary = georoutingLibrary,
        )
        val vpnManager = VpnManagerImpl(connectionStateRepository, dobbyVpnService)
        mainViewModel = MainViewModel(
            configsRepository = configsRepository,
            connectionStateRepository = connectionStateRepository,
            permissionEventsChannel = permissionEventsChannel,
            vpnManager = vpnManager,
            logger = logger,
            healthCheck = HealthCheckImpl(logger, healthCheckLibrary),
        )
    }

    fun runClient() {
        val path = Path.of(configPath)
        println("Reading config")
        val config = try {
            String(Files.readAllBytes(path))
        } catch (e: IOException) {
            println("Failed to read config: $e")
            return
        }

        runBlocking {
            val logJob = launch {
                logEventsChannel.logEvents.collect { line ->
                    println(line)
                }
            }

            launch {
                while (true) {
                    val line = readlnOrNull()
                    when (line) {
                        "vpn" -> {
                            println("Got vpn command")
                            mainViewModel.onConnectionButtonClicked(config)
                        }
                        "quit", null -> {
                            println("Got interruption command")
                            logJob.cancel()
                            exitProcess(0)
                        }
                        else -> {
                            println("Got invalid command command")
                        }
                    }
                }
            }


        }
    }
}
