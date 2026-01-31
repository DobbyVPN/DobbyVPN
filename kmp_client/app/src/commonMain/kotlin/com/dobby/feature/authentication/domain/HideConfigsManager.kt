package com.dobby.feature.authentication.domain

import com.russhwolf.settings.Settings
import dev.jordond.compass.permissions.PermissionState
import kotlinx.coroutines.Job
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import org.koin.core.component.KoinComponent
import org.koin.core.component.get

object HideConfigsManager: KoinComponent {
    val authenticationManager: AuthenticationManager = get()

    private val scope = MainScope()

    val settings = Settings()

    var triedBiometricAuth = false

    enum class TryEnableHideConfigsResult {
        SUCCESS, ERROR_NO_BIOMETRICS, ERROR_NO_LOCATION, IN_PROGRESS
    }

    fun tryEnableHideConfigs(endingFunc: (TryEnableHideConfigsResult) -> Job) {
        if (!authenticationManager.isAuthenticationAvailable()) {
            endingFunc(TryEnableHideConfigsResult.ERROR_NO_BIOMETRICS)
        }
        LocationManager.requestLocationPermission { res ->
            if (res == AuthPermissionState.Granted) {
                settings.putBoolean("isHideConfigsEnabled", true)
                endingFunc(TryEnableHideConfigsResult.SUCCESS)
            } else {
                endingFunc(TryEnableHideConfigsResult.ERROR_NO_LOCATION)
            }
        }
    }

    fun disableHideConfigs() = settings.putBoolean("isHideConfigsEnabled", false)

    fun isHideConfigsEnabled(): Boolean {
        if (!settings.hasKey("isHideConfigsEnabled")) {
            // when the user opens the app for the first time, try to enable the 'hide configurations' feature
            settings.putBoolean("isHideConfigsEnabled", false)
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
            if (triedBiometricAuth) {
                geoAuth(
                    onSuccess,
                    onFailure
                )
                return@launch
            }
            authenticationManager.authenticate(
                onAuthSuccess = {
                    onSuccess()
                    authStatus = AuthStatus.SUCCESS
                }, onAuthFailure = {
                    triedBiometricAuth = true
                    scope.launch {
                        geoAuth(
                            onSuccess,
                            onFailure
                        )
                    }
                }
            )
        }
    }

    fun geoAuth(
        onSuccess: () -> Unit,
        onFailure: () -> Unit
    ) {
        authenticationManager.requireLocationService { res ->
            if (!res) {
                onFailure()
                authStatus = AuthStatus.FAILURE
                return@requireLocationService
            }
            authenticationManager.requireLocationPermission {
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
        }
    }
}
