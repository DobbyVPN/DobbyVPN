package com.dobby.cli

import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.grpc.*
import kotlinx.coroutines.runBlocking

class CliClient {
    private val logsRepository: LogsRepository
    private val connectionStateRepository: ConnectionStateRepository
    private val configsRepository: DobbyConfigsRepository
    private val logger: Logger
    private val logEventsChannel: LogEventsChannel = LogEventsChannel()
    private val mainViewModel: MainViewModel

    init {
        logsRepository = LogsRepository(logEventsChannel = logEventsChannel)
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

    fun logs(options: List<String>): ExitCode =
        if (options.isEmpty()) {
            // Print logs
            logsRepository.readUILogs().forEach {
                println(it)
            }
            ExitCode.OK
        } else if (options.size == 2 && options[0] == "-n") {
            val count = options[1].toIntOrNull()
            if (count == null || count <= 0) {
                ExitCode.INVALID_ARGS
            } else {
                logsRepository.readLogs(count).forEach {
                    println(it)
                }
                ExitCode.OK
            }
        } else if (options.size == 1 && options[0] == "clear") {
            // Clear logs
            logsRepository.clearLogs()
            ExitCode.OK
        } else {
            ExitCode.INVALID_ARGS
        }

    fun connect(options: List<String>): ExitCode =
        if (options.isEmpty()) {
            // connect with healthcheck
            ExitCode.OK
        } else if (options.size == 1 && options[0] == "--skip-healthcheck") {
            // connect without healthcheck
            ExitCode.OK
        } else {
            ExitCode.INVALID_ARGS
        }

    fun disconnect(options: List<String>): ExitCode =
        if (options.isNotEmpty()) {
            ExitCode.INVALID_ARGS
        } else {
            runBlocking {
                mainViewModel.stopVpnService()
            }
            ExitCode.OK
        }

    fun status(options: List<String>): ExitCode =
        if (options.isEmpty()) {
            // Print status
            ExitCode.OK
        } else if (options.size == 1 && options[0] == "--json") {
            // Print json status
            ExitCode.OK
        } else {
            ExitCode.INVALID_ARGS
        }
}
