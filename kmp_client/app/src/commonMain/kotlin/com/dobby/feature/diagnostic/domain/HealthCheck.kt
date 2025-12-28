package com.dobby.feature.diagnostic.domain


interface HealthCheck {
    val timeToWakeUp: Int
    fun isConnected(): Boolean
}