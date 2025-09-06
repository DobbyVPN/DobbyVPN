package com.dobby.feature.main.domain

interface AwgManager {
    fun getAwgVersion(): String
    fun onAwgConnect()
    fun onAwgDisconnect()
}
