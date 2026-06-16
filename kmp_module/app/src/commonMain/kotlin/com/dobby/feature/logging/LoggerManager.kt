package com.dobby.feature.logging

interface LoggerManager {
    /**
     * Platform dependent logger initiation. Setups logger path, telemetry settings and attributes.
     */
    fun init()
}
