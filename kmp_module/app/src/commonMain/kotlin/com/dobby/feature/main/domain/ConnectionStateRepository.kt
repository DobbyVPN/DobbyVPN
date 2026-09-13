package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(VpnConnectionState.DISCONNECTED)
    val statusFlow = _statusFlow.asStateFlow()

    // Android's service callback is only a wake hint. Consumers fetch a fresh Go snapshot.
    private val _sessionChanges = MutableSharedFlow<Unit>(replay = 1, extraBufferCapacity = 1)
    val sessionChanges = _sessionChanges.asSharedFlow()

    suspend fun updateStatus(connectionState: VpnConnectionState) {
        _statusFlow.emit(connectionState)
    }

    fun publishSessionChanged() {
        _sessionChanges.tryEmit(Unit)
    }
}
