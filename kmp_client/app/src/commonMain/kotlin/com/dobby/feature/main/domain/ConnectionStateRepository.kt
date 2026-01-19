package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(false)
    val statusFlow = _statusFlow.asStateFlow()

    private val _vpnStartedFlow = MutableStateFlow(false)
    val vpnStartedFlow = _vpnStartedFlow.asStateFlow()

    suspend fun updateStatus(isConnected: Boolean) {
        _statusFlow.emit(isConnected)
    }

    fun tryUpdateStatus(isConnected: Boolean) {
        _statusFlow.tryEmit(isConnected)
    }

    suspend fun updateVpnStarted(isStarted: Boolean) {
        _vpnStartedFlow.emit(isStarted)
    }

    fun tryUpdateVpnStarted(isStarted: Boolean) {
        _vpnStartedFlow.tryEmit(isStarted)
    }
}
