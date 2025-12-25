package com.dobby.feature.diagnostic.domain

class HealthCheckImpl : HealthCheck {
    override fun isConnected(): Boolean {
        return true
    }
}