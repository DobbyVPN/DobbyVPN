package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow

class PermissionEventsChannel {

    private val _checkPermissionsEvents = MutableSharedFlow<Unit>()
    private val _permissionsGrantedEvents = MutableSharedFlow<Boolean>()

    val checkPermissionsEvents = _checkPermissionsEvents.asSharedFlow()
    val permissionsGrantedEvents = _permissionsGrantedEvents.asSharedFlow()

    suspend fun checkPermissions() {
        _checkPermissionsEvents.emit(Unit)
    }

    suspend fun onPermissionGranted(isGranted: Boolean) {
        _permissionsGrantedEvents.emit(isGranted)
    }
}
