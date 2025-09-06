package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

class ConnectionStateRepository {

    private val _connectionFlow = MutableStateFlow(false)

    val flow = _connectionFlow.asStateFlow()

    suspend fun update(isConnected: Boolean) {
        _connectionFlow.emit(isConnected)
    }

    fun tryUpdate(isConnected: Boolean) {
        _connectionFlow.tryEmit(isConnected)
    }
}
