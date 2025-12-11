package com.dobby.feature.authentication.domain

import com.russhwolf.settings.Settings
import dev.jordond.compass.permissions.PermissionState
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import org.koin.core.component.KoinComponent
import org.koin.core.component.get

object HideConfigsManager: KoinComponent {
    private val authenticationManager: AuthenticationManager = get()

    private val scope = MainScope()

    val settings = Settings()

    enum class TryEnableHideConfigsResult {
        SUCCESS, ERROR_NO_BIOMETRICS, ERROR_NO_LOCATION, IN_PROGRESS
    }

    suspend fun tryEnableHideConfigs(): TryEnableHideConfigsResult {
        if (!authenticationManager.isAuthenticationAvailable()) {
            return TryEnableHideConfigsResult.ERROR_NO_BIOMETRICS
        }
        return if (LocationManager.requestLocationPermission() == PermissionState.Granted) {
            settings.putBoolean("isHideConfigsEnabled", true)
            TryEnableHideConfigsResult.SUCCESS
        } else {
            TryEnableHideConfigsResult.ERROR_NO_LOCATION
        }
    }

    fun disableHideConfigs() = settings.putBoolean("isHideConfigsEnabled", false)

    suspend fun isHideConfigsEnabled(): Boolean {
        if (!settings.hasKey("isHideConfigsEnabled")) {
            // when the user opens the app for the first time, try to enable the 'hide configurations' feature
            settings.putBoolean("isHideConfigsEnabled", tryEnableHideConfigs() == TryEnableHideConfigsResult.SUCCESS)
        }
        return settings.getBoolean("isHideConfigsEnabled", false)
    }

    enum class AuthStatus {
        NONE, IN_PROGRESS, SUCCESS, FAILURE
    }
    var authStatus: AuthStatus = AuthStatus.NONE
         set(value) {
            field = value
            scope.launch {
                _authState.emit(field)
            }
        }

    private val _authState: MutableStateFlow<AuthStatus> = MutableStateFlow(authStatus)
    val authState: StateFlow<AuthStatus> = _authState

    fun authenticate(
        onSuccess: () -> Unit,
        onFailure: () -> Unit
    ) {
        if (authStatus != AuthStatus.NONE) {
            return
        }
        authStatus = AuthStatus.IN_PROGRESS
        scope.launch {
            if (!isHideConfigsEnabled()) {
                onSuccess()
                authStatus = AuthStatus.SUCCESS
                return@launch
            }
            // if 'hide configurations' feature is enabled, show the authentication prompt
            authenticationManager.authenticate(
                onAuthSuccess = {
                    onSuccess()
                    authStatus = AuthStatus.SUCCESS
                }, onAuthFailure = {
                    scope.launch {
                        val inRedZone = LocationManager.inRedZone()
                        if (inRedZone == RedZoneCheckResult.NOT_RED_ZONE) {
                            onSuccess()
                            authStatus = AuthStatus.SUCCESS
                        } else {
                            onFailure()
                            authStatus = AuthStatus.FAILURE
                        }
                    }
                }
            )
        }

    }
}
