package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow<VpnConnectionState>(VpnConnectionState.DISCONNECTED)
    val statusFlow = _statusFlow.asStateFlow()

    private val _vpnStartedFlow = MutableStateFlow(false)
    val vpnStartedFlow = _vpnStartedFlow.asStateFlow()

    suspend fun updateStatus(connectionState: VpnConnectionState) {
        _statusFlow.emit(connectionState)
    }

    fun tryUpdateStatus(connectionState: VpnConnectionState) {
        _statusFlow.tryEmit(connectionState)
    }

    suspend fun updateVpnStarted(isStarted: Boolean) {
        _vpnStartedFlow.emit(isStarted)
    }

    fun tryUpdateVpnStarted(isStarted: Boolean) {
        _vpnStartedFlow.tryEmit(isStarted)
    }
}
