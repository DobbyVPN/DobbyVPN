package com.dobby.feature.authentication.domain

import android.content.Context
import android.content.Intent
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_WEAK
import androidx.biometric.BiometricPrompt
import androidx.biometric.BiometricPrompt.ERROR_NEGATIVE_BUTTON
import android.widget.Toast
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import dev.jordond.compass.Priority
import dev.jordond.compass.permissions.PermissionState
import kotlinx.coroutines.Job
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.launch
import android.provider.Settings
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner

private lateinit var activity : FragmentActivity

fun initBiometricAuthenticationManager(context: FragmentActivity) {
    activity = context
}

class AuthenticationManagerImpl(
    private val context: Context
): AuthenticationManager {
    override fun isAuthenticationAvailable() = (BiometricManager.from(context).canAuthenticate(BIOMETRIC_WEAK)
            == BiometricManager.BIOMETRIC_SUCCESS)

    override fun authenticate(
        onAuthSuccess: () -> Unit,
        onAuthFailure: () -> Unit
    ) {
        if (!isAuthenticationAvailable()) {
            onAuthSuccess()
            return
        }

        val biometricPrompt = BiometricPrompt(
            activity,
            ContextCompat.getMainExecutor(context),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationError(
                    errorCode: Int,
                    errString: CharSequence
                ) {
                    super.onAuthenticationError(errorCode, errString)
                    if (errorCode != ERROR_NEGATIVE_BUTTON) {
                        Toast.makeText(
                            context,
                            "Authentication error: $errString",
                            Toast.LENGTH_SHORT
                        )
                            .show()
                    }
                    onAuthFailure()
                }

                override fun onAuthenticationSucceeded(
                    result: BiometricPrompt.AuthenticationResult
                ) {
                    super.onAuthenticationSucceeded(result)
                    onAuthSuccess()
                }

                override fun onAuthenticationFailed() {
                    super.onAuthenticationFailed()
                    onAuthFailure()
                }
            })


        val promptInfo = BiometricPrompt.PromptInfo.Builder()
            .setTitle("Biometric login")
            .setConfirmationRequired(false)
            .setNegativeButtonText("Cancel")
            .build()

        biometricPrompt.authenticate(promptInfo)
    }

    override fun requireLocationPermission(endingFunc: (AuthPermissionState) -> Job) {
        val controller = locationPermissionController ?: return

        MainScope().launch {
            val status = controller.requirePermissionFor(Priority.HighAccuracy)

            val state: AuthPermissionState = when (status) {
                PermissionState.Granted -> AuthPermissionState.Granted
                PermissionState.Denied -> AuthPermissionState.Denied
                PermissionState.NotDetermined -> AuthPermissionState.NotDetermined
                PermissionState.DeniedForever -> AuthPermissionState.NotDetermined
            }
            endingFunc(state)
        }
    }

    override fun requireLocationService(endingFunc: (Boolean) -> Unit) {
        val locationManager =
            context.getSystemService(Context.LOCATION_SERVICE) as android.location.LocationManager

        fun isGpsEnabled(): Boolean {
            return locationManager.isProviderEnabled(android.location.LocationManager.GPS_PROVIDER) ||
                    locationManager.isProviderEnabled(android.location.LocationManager.NETWORK_PROVIDER)
        }

        if (isGpsEnabled()) {
            endingFunc(true)
            return
        }

        val dialog = android.app.AlertDialog.Builder(activity)
            .setTitle("Enable location")
            .setMessage("Location services are turned off. Please enable them to continue.")
            .setCancelable(true)
            .setPositiveButton("Open settings") { _, _ ->

                        val lifecycle = activity.lifecycle
                val observer = object : DefaultLifecycleObserver {
                    override fun onResume(owner: LifecycleOwner) {
                        super.onResume(owner)

                        lifecycle.removeObserver(this)

                        endingFunc(isGpsEnabled())
                    }
                }

                lifecycle.addObserver(observer)

                try {
                    activity.startActivity(Intent(Settings.ACTION_LOCATION_SOURCE_SETTINGS))
                } catch (e: Exception) {
                    activity.startActivity(Intent(Settings.ACTION_SETTINGS))
                }
            }
            .setNegativeButton("Cancel") { _, _ ->
                endingFunc(false)
            }
            .create()

        dialog.show()
    }


}