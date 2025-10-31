package com.dobby.feature.main.domain

interface AuthenticationManager {
    fun authenticate(
        onAuthSuccess: () -> Unit,
        onAuthFailure: () -> Unit
    )
}