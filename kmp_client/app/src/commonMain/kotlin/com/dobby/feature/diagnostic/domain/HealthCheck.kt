package com.dobby.feature.diagnostic.domain


interface HealthCheck {
    fun isConnected(): Boolean
}