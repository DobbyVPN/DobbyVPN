package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow

class SessionChangeEvents {
    // Android's service callback is only a wake hint. Consumers fetch a fresh Go snapshot.
    private val _sessionChanges = MutableSharedFlow<Unit>(replay = 1, extraBufferCapacity = 1)
    val sessionChanges = _sessionChanges.asSharedFlow()

    fun publishSessionChanged() {
        _sessionChanges.tryEmit(Unit)
    }
}
