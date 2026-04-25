package com.dobby.feature.main.domain

interface LoggerManager {
    fun initLogger()
    fun initTelemetry(endpoint: String)
}
