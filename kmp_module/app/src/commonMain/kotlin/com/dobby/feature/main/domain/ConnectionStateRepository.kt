package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(VpnConnectionState.DISCONNECTED)
    val statusFlow = _statusFlow.asStateFlow()

    private val _serviceStartedFlow = MutableStateFlow(false)
    val serviceStartedFlow = _serviceStartedFlow.asStateFlow()

    suspend fun updateStatus(connectionState: VpnConnectionState) {
        _statusFlow.emit(connectionState)
    }

    fun tryUpdateStatus(connectionState: VpnConnectionState) {
        _statusFlow.tryEmit(connectionState)
    }

    suspend fun updateServiceStarted(isStarted: Boolean) {
        _serviceStartedFlow.emit(isStarted)
    }

    fun tryUpdateServiceStarted(isStarted: Boolean) {
        _serviceStartedFlow.tryEmit(isStarted)
    }
}
