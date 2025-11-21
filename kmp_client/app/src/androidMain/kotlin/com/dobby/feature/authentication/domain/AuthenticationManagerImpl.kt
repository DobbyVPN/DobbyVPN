package com.dobby.feature.authentication.domain

import android.content.Context
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_WEAK
import androidx.biometric.BiometricPrompt
import androidx.biometric.BiometricPrompt.ERROR_NEGATIVE_BUTTON
import android.widget.Toast
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity

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
}