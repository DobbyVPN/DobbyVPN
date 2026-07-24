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

    suspend fun updateServiceStarted(isStarted: Boolean, generation: Long = 0L) {
        serviceStartedFlow.emit(isStarted, generation)
    }

    fun tryUpdateServiceStarted(isStarted: Boolean, generation: Long = 0L) {
        serviceStartedFlow.tryEmit(isStarted, generation)
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
 * [ServiceStarted.prepare] -> generation-tagged platform request -> [ServiceStarted.awaitResult] ->
 * what will block coroutine scope until we receive the result from the VPN service.
 */
class ServiceStarted {
    private data class Result(val started: Boolean, val generation: Long)
    private val result = Channel<Result>(capacity = Channel.UNLIMITED)

    fun prepare(generation: Long = 0L) {
        while (!result.tryReceive().isFailure) {
            // Drain stale start results before a new launch attempt.
        }
    }

    suspend fun emit(started: Boolean, generation: Long = 0L) {
        result.send(Result(started, generation))
    }

    fun tryEmit(started: Boolean, generation: Long = 0L) {
        result.trySend(Result(started, generation))
    }

    suspend fun awaitResult(timeoutMs: Long, generation: Long = 0L): Boolean {
        return withTimeoutOrNull(timeoutMs) {
            awaitGeneration(generation)
        } ?: false
    }

    private suspend fun awaitGeneration(generation: Long): Boolean {
        while (true) {
            val next = result.receive()
            if (next.generation == generation) {
                return next.started
            }
        }
    }
}
