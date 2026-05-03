package com.dobby.feature.netcheck

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.netcheck.presentation.NetCheckManager
import interop.logger.LoggerLibrary
import interop.netcheck.NetCheckLibrary

class NetCheckManagerImpl(
    private val logger: Logger,
    private val configsRepository: DobbyConfigsRepository,
    private val loggerLibrary: LoggerLibrary,
    private val netCheckLibrary: NetCheckLibrary,
) : NetCheckManager {
    fun enableTunnelLogging() {
        val logFilePath = provideLogFilePath()
        logger.log("Init tunnel logging to the path: $logFilePath")
        loggerLibrary.InitLogger(logFilePath.toString())
    }

    fun enableTunnelTelemetry() {
        val endpoint = configsRepository.getTelemetryEndpoint()
        logger.log("Init tunnel telemetry to the endpoint: $endpoint")
        if (endpoint.isNotBlank()) {
            loggerLibrary.InitTelemetry(endpoint)
        } else {
            logger.log("No telemetry endpoint provided")
        }
    }

    override fun start(configPath: String): String {
        enableTunnelLogging()
        enableTunnelTelemetry()
        val path = provideLogFilePath().toString()
        loggerLibrary.InitLogger(path)
        return netCheckLibrary.NetCheck(configPath)
    }

    override fun cancel() {
        netCheckLibrary.CancelNetCheck()

    }
}
