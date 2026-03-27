package com.dobby.feature.diagnostic.domain


interface HealthCheck {
    fun shortConnectionCheckUp(): Boolean
    fun fullConnectionCheckUp(): Boolean
    fun checkServerAlive(address: String, port: Int): Boolean
    fun getTimeToWakeUp(): Int
}