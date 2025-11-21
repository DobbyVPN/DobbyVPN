package com.dobby.feature.authentication.domain

class AuthenticationManagerImpl: AuthenticationManager {
    override fun isAuthenticationAvailable() = false

    override fun authenticate(
        onAuthSuccess: () -> Unit,
        onAuthFailure: () -> Unit
    ) {
        onAuthSuccess()
    }
}