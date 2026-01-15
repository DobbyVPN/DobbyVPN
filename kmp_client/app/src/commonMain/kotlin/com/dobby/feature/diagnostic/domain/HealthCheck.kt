package com.dobby.feature.diagnostic.domain


interface HealthCheck {
    fun isConnected(): Boolean
    fun checkServerAlive(address: String, port: Int): Boolean
    fun getTimeToWakeUp(): Int
}