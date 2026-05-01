package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

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
        serviceStartedFlow.emit(isStarted)
    }
}

class ServiceStarted {
    private var value: Boolean? = null
    private var callback: ((Boolean) -> Unit)? = null

    fun prepare() {
        value = null
        callback = null
    }

    fun emit(started: Boolean) {
        val currentCallback = callback
        currentCallback?.invoke(started)
        value = started
    }

    fun collect(function: (Boolean) -> Unit) {
        val currentValue = value
        if (currentValue != null) {
            function.invoke(currentValue)
        }
        callback = function
    }

}
