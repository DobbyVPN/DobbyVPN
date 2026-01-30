package com.dobby.feature.authentication.domain

import kotlinx.coroutines.Job

enum class AuthPermissionState {
    Granted,
    Denied,
    NotDetermined
}


interface AuthenticationManager {
    fun isAuthenticationAvailable(): Boolean
    fun authenticate(
        onAuthSuccess: () -> Unit,
        onAuthFailure: () -> Unit
    )
    fun requireLocationPermission(endingFunc: (AuthPermissionState) -> Job)
}