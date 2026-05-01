package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(VpnConnectionState.DISCONNECTED)
    val statusFlow = _statusFlow.asStateFlow()

    val serviceStartedFlow = ServiceStarted()

    suspend fun updateStatus(connectionState: VpnConnectionState) {
        _statusFlow.emit(connectionState)
    }

    fun tryUpdateStatus(connectionState: VpnConnectionState) {
        _statusFlow.tryEmit(connectionState)
    }

    suspend fun updateServiceStarted(isStarted: Boolean) {
        serviceStartedFlow.emit(isStarted)
    }

    fun tryUpdateServiceStarted(isStarted: Boolean) {
        serviceStartedFlow.tryEmit(isStarted)
    }
}

class ServiceStarted {
    private val value = MutableStateFlow<Boolean?>(null)

    suspend fun prepare() {
        value.emit(null)
    }

    suspend fun emit(started: Boolean) {
        value.emit(started)
    }

    fun tryEmit(started: Boolean) {
        value.tryEmit(started)
    }

    suspend fun awaitResult(): Boolean {
        val connected = value.first { it != null }
        return connected!!
    }

}
