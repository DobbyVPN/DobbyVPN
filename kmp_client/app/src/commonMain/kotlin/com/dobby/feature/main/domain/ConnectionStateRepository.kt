package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(false)
    val statusFlow = _statusFlow.asStateFlow()

    private val _vpnStartedFlow = MutableStateFlow(false)
    val vpnStartedFlow = _vpnStartedFlow.asStateFlow()

    private val _restartPendingFlow = MutableStateFlow(false)
    val restartPendingFlow = _restartPendingFlow.asStateFlow()

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

    suspend fun updateRestartPending(isPending: Boolean) {
        _restartPendingFlow.emit(isPending)
    }

    fun tryUpdateRestartPending(isPending: Boolean) {
        _restartPendingFlow.tryEmit(isPending)
    }
}
