package com.dobby.feature.logging

interface LoggerManager {
    /**
     * Platform dependent logger initiation. Setups logger path, telemetry settings and attributes.
     */
    fun initLogger()

    /** Tears down OTLP telemetry for the current VPN session. */
    fun stopTelemetry()
}
