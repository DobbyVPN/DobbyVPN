package com.dobby.feature.logging

interface LoggerManager {
    /**
     * Platform dependent logger initiation. Logs remain local-only.
     */
    fun initLogger()

    /** Compatibility no-op: there is no remote telemetry exporter. */
    fun stopTelemetry()
}
