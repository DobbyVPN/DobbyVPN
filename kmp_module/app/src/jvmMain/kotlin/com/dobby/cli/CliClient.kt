package com.dobby.cli

import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckManagerImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.main.presentation.ConfigsProcessor
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.grpc.*
import korlibs.time.DateTime
import korlibs.time.seconds
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import java.io.File
import java.nio.charset.Charset
import java.nio.file.Files
import kotlin.time.Duration.Companion.milliseconds

class CliClient {
    private val healthCheckLibrary: RestartableHealthCheckGrpcLibrary
    private val logsRepository: LogsRepository
    private val mainViewModel: MainViewModel

    init {
        val logEventsChannel = LogEventsChannel()
        logsRepository = LogsRepository(logEventsChannel = logEventsChannel)
        val logger = Logger(logsRepository)

        healthCheckLibrary = RestartableHealthCheckGrpcLibrary(logger)
        val awgLibrary = RestartableAwgGrpcLibrary(logger)
        val outlineLibrary = RestartableOutlineGrpcLibrary(logger)
        val xrayLibrary = RestartableXrayGrpcLibrary(logger)
        val cloakLibrary = RestartableCloakGrpcLibrary(logger)
        val loggerLibrary = RestartableLoggerGrpcLibrary(logger)
        val georoutingLibrary = RestartableGeoroutingGrpcLibrary(logger)

        val configsRepository = DobbyConfigsRepositoryImpl(healthCheckLibrary = healthCheckLibrary)
        val connectionStateRepository = ConnectionStateRepository()
        val permissionEventsChannel = PermissionEventsChannel()
        val dobbyVpnService = DobbyVpnService(
            dobbyConfigsRepository = configsRepository,
            logger = logger,
            logsRepository = logsRepository,
            awgLibrary = awgLibrary,
            outlineLibrary = outlineLibrary,
            xrayLibrary = xrayLibrary,
            cloakLibrary = cloakLibrary,
            georoutingLibrary = georoutingLibrary,
        )
        val vpnManager = VpnManagerImpl(connectionStateRepository, dobbyVpnService)
        mainViewModel = MainViewModel(
            configsRepository = configsRepository,
            connectionStateRepository = connectionStateRepository,
            permissionEventsChannel = permissionEventsChannel,
            vpnManager = vpnManager,
            loggerManager = LoggerManagerImpl(logger, loggerLibrary, configsRepository),
            logger = logger,
            logsRepository = logsRepository,
            healthCheckManager = HealthCheckManagerImpl(logger, healthCheckLibrary),
            configsProcessor = ConfigsProcessor(configsRepository),
        )
    }

    fun logs(options: List<String>): ExitCode = if (options.isEmpty()) {
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

    private suspend fun awaitHealthCheck(): Boolean {
        val initialTime = DateTime.now()
        var time = initialTime

        while (time < initialTime + 15.seconds) {
            time = DateTime.now()
            when (healthCheckLibrary.GetConnectionState()) {
                0, 1 -> delay(200.milliseconds)
                2 -> return true
                else -> return false
            }
        }

        return false
    }

    fun connect(options: List<String>): ExitCode {
        val skipHealthCheck: Boolean = when (options.size) {
            1 -> false
            2 if options[1] == "--skip-healthcheck" -> true
            else -> return ExitCode.INVALID_ARGS
        }

        val filePath = options[0]
        val connectionUrl = if (filePath.isValidUrl()) {
            filePath
        } else {
            val path = File(filePath).toPath()
            val charset = Charset.forName("utf-8")
            runCatching {
                Files.readString(path, charset)
            }.getOrElse { return ExitCode.INVALID_ARGS }
        }

        val okConfig = runBlocking { mainViewModel.setConfig(connectionUrl) }
        if (!okConfig) {
            return ExitCode.CONFIG_FORMAT_ERROR
        }

        val okVpn = runBlocking { mainViewModel.startVpnService() }
        if (!okVpn) {
            return ExitCode.TUNNEL_START_ERROR
        }

        if (skipHealthCheck) {
            return ExitCode.OK
        }

        val okHC = runBlocking { awaitHealthCheck() }
        if (!okHC) {
            return ExitCode.HEALTHCHECK_CONFIG_ERROR
        }

        return ExitCode.OK
    }

    fun disconnect(options: List<String>): ExitCode = if (options.isNotEmpty()) {
        ExitCode.INVALID_ARGS
    } else {
        runBlocking {
            mainViewModel.stopVpnService()
        }
        ExitCode.OK
    }

    fun status(options: List<String>): ExitCode {
        return if (options.isEmpty()) {
            // Print status
            when (healthCheckLibrary.GetConnectionState()) {
                0 -> println("Disconnected")
                1 -> println("Connecting")
                2 -> println("Connected")
                else -> return ExitCode.PROGRAM_FAILED
            }
            ExitCode.OK
        } else if (options.size == 1 && options[0] == "--json") {
            // Print json status
            when (healthCheckLibrary.GetConnectionState()) {
                0 -> println("{ \"code\": 0, \"state\": \"Disconnected\" }")
                1 -> println("{ \"code\": 1, \"state\": \"Connecting\" }")
                2 -> println("{ \"code\": 2, \"state\": \"Connected\" }")
                else -> return ExitCode.PROGRAM_FAILED
            }
            ExitCode.OK
        } else {
            ExitCode.INVALID_ARGS
        }
    }
}

fun String.isValidUrl(): Boolean {
    return try {
        val url = java.net.URL(this)
        url.toURI() // Ensures proper URI format
        true
    } catch (_: Exception) {
        false
    }
}
