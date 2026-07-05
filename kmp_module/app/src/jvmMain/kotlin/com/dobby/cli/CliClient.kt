package com.dobby.cli

import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckManager
import com.dobby.feature.diagnostic.domain.HealthCheckManagerImpl
import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.LoggerManager
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionProfile
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.main.domain.DnsPreflightResolverImpl
import com.dobby.feature.main.domain.config.ConnectionProfileApplier
import com.dobby.feature.main.domain.config.ConnectionProfileStore
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
    private val logger: Logger
    private val configsRepository: DobbyConfigsRepository
    private val connectionStateRepository: ConnectionStateRepository
    private val vpnManager: VpnManager
    private val loggerManager: LoggerManager
    private val healthCheckManager: HealthCheckManager
    private val configsProcessor: ConfigsProcessor
    private val profileStore: ConnectionProfileStore
    private val profileApplier: ConnectionProfileApplier
    private val mainViewModel: MainViewModel

    init {
        val logEventsChannel = LogEventsChannel()
        logsRepository = LogsRepository(logEventsChannel = logEventsChannel)
        logger = Logger(logsRepository)

        healthCheckLibrary = RestartableHealthCheckGrpcLibrary(logger)
        val awgLibrary = RestartableAwgGrpcLibrary(logger)
        val outlineLibrary = RestartableOutlineGrpcLibrary(logger)
        val xrayLibrary = RestartableXrayGrpcLibrary(logger)
        val cloakLibrary = RestartableCloakGrpcLibrary(logger)
        val dnsCacheLibrary = RestartableDnsCacheGrpcLibrary(logger)
        val loggerLibrary = RestartableLoggerGrpcLibrary(logger)
        val georoutingLibrary = RestartableGeoroutingGrpcLibrary(logger)

        configsRepository = DobbyConfigsRepositoryImpl(healthCheckLibrary = healthCheckLibrary)
        connectionStateRepository = ConnectionStateRepository()
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
        vpnManager = VpnManagerImpl(connectionStateRepository, dobbyVpnService)
        loggerManager = LoggerManagerImpl(logger, loggerLibrary, configsRepository)
        healthCheckManager = HealthCheckManagerImpl(logger, healthCheckLibrary)
        configsProcessor = ConfigsProcessor(configsRepository)
        profileStore = ConnectionProfileStore(configsRepository, logger)
        profileApplier = ConnectionProfileApplier(configsRepository, logger)
        mainViewModel = MainViewModel(
            configsRepository = configsRepository,
            connectionStateRepository = connectionStateRepository,
            permissionEventsChannel = permissionEventsChannel,
            vpnManager = vpnManager,
            loggerManager = loggerManager,
            logger = logger,
            logsRepository = logsRepository,
            healthCheckManager = healthCheckManager,
            configsProcessor = configsProcessor,
            dnsPreflightResolver = DnsPreflightResolverImpl(dnsCacheLibrary, logger),
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

    private suspend fun awaitHealthCheck(timeoutSeconds: Int = DEFAULT_HEALTHCHECK_TIMEOUT_SECONDS): Boolean {
        val initialTime = DateTime.now()
        var time = initialTime

        while (time < initialTime + timeoutSeconds.seconds) {
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

        val connectionUrl = readConnectionArgument(options[0]) ?: return ExitCode.INVALID_ARGS

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

    fun checkConfig(options: List<String>): ExitCode {
        if (options.size != 1) {
            return ExitCode.INVALID_ARGS
        }

        val connectionUrl = readConnectionArgument(options[0]) ?: return ExitCode.INVALID_ARGS

        val okConfig = runBlocking { mainViewModel.setConfig(connectionUrl) }
        if (!okConfig) {
            return ExitCode.CONFIG_FORMAT_ERROR
        }

        val profiles = profileStore.getProfiles()
        if (profiles.isEmpty()) {
            return ExitCode.CONFIG_FORMAT_ERROR
        }

        return runBlocking {
            checkProfiles(profiles)
        }
    }

    private suspend fun checkProfiles(profiles: List<ConnectionProfile>): ExitCode {
        var failures = 0

        for ((index, profile) in profiles.withIndex()) {
            val label = profile.label(index, profiles.size)
            println("Checking $label")
            logger.log("[CLI] Checking $label")

            configsRepository.setActiveConnectionProfileIndex(index)
            val applied = runCatching {
                profileApplier.apply(profile)
            }.getOrElse { error ->
                logger.log("[CLI] FAILED $label: profile config could not be applied: ${error.message}")
                false
            }
            if (!applied) {
                failures += 1
                println("FAILED $label: profile config could not be applied")
                logger.log("[CLI] FAILED $label: profile config could not be applied")
                continue
            }

            configsRepository.setTelemetryAttributes(configsProcessor.buildConfigAttributesJson())

            try {
                val started = startVpnServiceOnce()
                if (!started) {
                    failures += 1
                    println("FAILED $label: VPN tunnel did not start")
                    logger.log("[CLI] FAILED $label: VPN tunnel did not start")
                    printRecentLogs()
                    continue
                }

                val healthy = awaitHealthCheck(PROFILE_HEALTHCHECK_TIMEOUT_SECONDS)
                if (!healthy) {
                    failures += 1
                    val state = connectionStateDescription(healthCheckLibrary.GetConnectionState())
                    println("FAILED $label: healthcheck did not report Connected, state=$state")
                    logger.log("[CLI] FAILED $label: healthcheck did not report Connected, state=$state")
                    printRecentLogs()
                    continue
                }

                println("OK $label")
                logger.log("[CLI] OK $label")
            } finally {
                stopVpnRuntime()
            }
        }

        println("Checked ${profiles.size} profile(s), failures=$failures")

        return if (failures > 0) {
            ExitCode.PROTOCOL_CHECK_FAILED
        } else {
            ExitCode.OK
        }
    }

    private suspend fun startVpnServiceOnce(): Boolean {
        connectionStateRepository.updateStatus(VpnConnectionState.CONNECTING)
        healthCheckManager.initHealthCheck()
        logsRepository.cleanupOldLogs()
        loggerManager.initLogger()
        connectionStateRepository.serviceStartedFlow.prepare()
        vpnManager.start(isProtocolProbe = false)

        val connected = connectionStateRepository.serviceStartedFlow.awaitResult(SERVICE_START_TIMEOUT_MS)
        if (connected) {
            healthCheckManager.startHealthCheck()
        }
        return connected
    }

    private fun stopVpnRuntime() {
        vpnManager.stop()
        healthCheckManager.stopHealthCheck()
        connectionStateRepository.tryUpdateStatus(VpnConnectionState.DISCONNECTED)
    }

    private fun printRecentLogs() {
        println("Recent logs:")
        logsRepository.readLogs(20).forEach {
            println(it)
        }
    }

    private fun readConnectionArgument(value: String): String? {
        if (value.isValidUrl()) {
            return value
        }

        val path = File(value).toPath()
        val charset = Charset.forName("utf-8")
        return runCatching {
            Files.readString(path, charset)
        }.getOrNull()
    }

    private fun ConnectionProfile.label(index: Int, total: Int): String {
        val descriptionPart = description
            ?.replace(Regex("\\s+"), " ")
            ?.trim()
            ?.takeIf { it.isNotEmpty() }
            ?.let { "description=\"$it\", " }
            .orEmpty()
        return "profile ${index + 1}/$total: ${descriptionPart}protocol=$protocol, sourceIndex=$sourceIndex"
    }

    private fun connectionStateDescription(code: Int): String =
        when (code) {
            0 -> "Disconnected"
            1 -> "Connecting"
            2 -> "Connected"
            else -> "Unknown($code)"
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

    private companion object {
        const val DEFAULT_HEALTHCHECK_TIMEOUT_SECONDS = 15
        const val PROFILE_HEALTHCHECK_TIMEOUT_SECONDS = 30
        const val SERVICE_START_TIMEOUT_MS = 90_000L
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
