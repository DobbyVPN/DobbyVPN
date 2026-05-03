package com.dobby.cli

import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.grpc.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path

class CliClient {
    private val connectionStateRepository: ConnectionStateRepository
    private val configsRepository: DobbyConfigsRepository
    private val logger: Logger
    private val logEventsChannel: LogEventsChannel = LogEventsChannel()
    private val mainViewModel: CliMainViewModel

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
        val connectionState = ConnectionStateRepository()
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
        val vpnManager = VpnManagerImpl(dobbyVpnService)
        mainViewModel = CliMainViewModel(
            configsRepository = configsRepository,
            connectionStateRepository = connectionStateRepository,
            permissionEventsChannel = permissionEventsChannel,
            vpnManager = vpnManager,
            logger = logger,
            healthCheck = HealthCheckImpl(logger, healthCheckLibrary),
        )
    }

    fun connect(configPath: String) {
        val path = Path.of(configPath)
        println("Reading config")
        val config = try {
            String(Files.readAllBytes(path))
        } catch (e: IOException) {
            println("Failed to read config: $e")
            return
        }

        println("Connecting")

        logger.log("The connection button was clicked with URL: ${maskStr(config)}")

        if (!configsRepository.couldStart()) {
            logger.log("We couldn't do this operation, configsRepository.couldStart() returned FALSE")
            return
        }

        runBlocking {
            launch(Dispatchers.Default) {
                mainViewModel.prepareConfig(config)
                configsRepository.setIsUserInitStop(false)
                mainViewModel.connect()
            }
        }

        logs()
    }

    fun disconnect() {
        runBlocking {
            launch(Dispatchers.Default) {
                configsRepository.setIsUserInitStop(true)
                mainViewModel.disconnect()
            }
        }

        logs()
    }

    fun logs() {
        println("Running log print")
        runBlocking {
            logEventsChannel.logEvents.collect { line ->
                println(line)
            }
        }
    }
}
