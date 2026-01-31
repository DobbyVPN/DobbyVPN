package com.dobby.feature.authentication.domain

import kotlinx.coroutines.Job

class AuthenticationManagerImpl: AuthenticationManager {
    override fun isAuthenticationAvailable() = false

    override fun authenticate(
        onAuthSuccess: () -> Unit,
        onAuthFailure: () -> Unit
    ) {
        onAuthSuccess()
    }

    override fun requireLocationPermission(endingFunc: (AuthPermissionState) -> Job) {
    }

    override fun requireLocationService(endingFunc: (Boolean) -> Unit) {
    }
}