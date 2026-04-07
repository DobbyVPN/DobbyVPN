package com.dobby.feature.netcheck.presentation

interface NetCheckManager {
    fun start(configPath: String): String
    fun cancel()
}
