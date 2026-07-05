package com.dobby.feature.main.domain

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withTimeoutOrNull

class ConnectionStateRepository {
    private val _statusFlow = MutableStateFlow(VpnConnectionState.DISCONNECTED)
    val statusFlow = _statusFlow.asStateFlow()

    val serviceStartedFlow = ServiceStarted()
    val vpnNetworkReadyFlow = ServiceStarted()

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

    fun tryUpdateVpnNetworkReady(isReady: Boolean) {
        vpnNetworkReadyFlow.tryEmit(isReady)
    }
}

/**
 * A helper class for more explicit passing of the
 * VPN service launch result from the service itself to [com.dobby.feature.main.presentation.MainViewModel].
 *
 * **Expected usage**:
 *
 * [ServiceStarted.prepare] -> [VpnManager.start] -> [ServiceStarted.awaitResult] ->
 * what will block coroutine scope until we receive the result from the VPN service.
 */
class ServiceStarted {
    private val result = Channel<Boolean>(capacity = Channel.CONFLATED)

    fun prepare() {
        while (!result.tryReceive().isFailure) {
            // Drain stale start results before a new launch attempt.
        }
    }

    suspend fun emit(started: Boolean) {
        result.send(started)
    }

    fun tryEmit(started: Boolean) {
        result.trySend(started)
    }

    suspend fun awaitResult(timeoutMs: Long): Boolean {
        return withTimeoutOrNull(timeoutMs) {
            result.receive()
        } ?: false
    }
}
