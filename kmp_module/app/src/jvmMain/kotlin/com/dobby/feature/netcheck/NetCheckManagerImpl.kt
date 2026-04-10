package com.dobby.feature.netcheck

import com.dobby.feature.logging.domain.provideLogFilePath
import com.dobby.feature.netcheck.presentation.NetCheckManager
import interop.logger.LoggerLibrary
import interop.netcheck.NetCheckLibrary

class NetCheckManagerImpl(
    private val loggerLibrary: LoggerLibrary,
    private val netCheckLibrary: NetCheckLibrary,
) : NetCheckManager {
    override fun start(configPath: String): String {
        val path = provideLogFilePath().toString()
        loggerLibrary.InitLogger(path)
        return netCheckLibrary.NetCheck(configPath)
    }

    override fun cancel() {
        netCheckLibrary.CancelNetCheck()

    }
}
