package com.dobby.feature.authentication.domain

interface AuthenticationManager {
    fun isAuthenticationAvailable(): Boolean
    fun authenticate(
        onAuthSuccess: () -> Unit,
        onAuthFailure: () -> Unit
    )
}