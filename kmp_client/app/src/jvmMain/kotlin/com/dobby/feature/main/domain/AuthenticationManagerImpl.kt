package com.dobby.feature.main.domain

class AuthenticationManagerImpl: AuthenticationManager {
    override fun authenticate(onAuthSuccess: () -> Unit) {
        onAuthSuccess()
    }
}