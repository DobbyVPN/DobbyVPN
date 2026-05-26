package com.dobby.feature.netcheck.presentation

interface NetCheckManager {
    fun startNetCheck(): String
    fun cancelNetCheck()
}
