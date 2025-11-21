package com.dobby.feature.authentication.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.dobby.feature.authentication.domain.HideConfigsManager
import com.dobby.feature.authentication.presentation.AuthenticationSettingsViewModel
import com.dobby.util.koinViewModel

@Composable
fun AuthenticationSettingsScreen(
    authenticationSettingsViewModel: AuthenticationSettingsViewModel = koinViewModel(),
) {
    val isHideConfigsEnabled by authenticationSettingsViewModel.hideConfigsSettingState.collectAsState()
    val tryEnableHideConfigsStatus by authenticationSettingsViewModel.tryEnableHideConfigsStatus.collectAsState()

    Column(
        modifier = Modifier.fillMaxWidth().padding(20.dp)
    ) {
        Text(
            text = if (isHideConfigsEnabled) {
                "Hide configurations: ON"
            } else {
                "Hide configurations: OFF"
            },
            fontSize = MaterialTheme.typography.bodyMedium.fontSize,
            fontWeight = FontWeight.Bold,
        )
        Spacer(Modifier.padding(5.dp))
        Text(
            text = "Hide your configurations when in potentially dangerous areas. " +
                "You need to have set up biometric authentication (face unlock, fingerprint etc.) on your device to enable this setting. " +
                "The app will ask you to authenticate to access your configurations. " +
                "This feature uses location services on your device to determine whether you are in a potentially dangerous area, " +
                "you will have to grant this app the permission to access your location to enable it.",
            fontSize = MaterialTheme.typography.bodyMedium.fontSize,
            fontWeight = FontWeight.Normal,
        )
        Spacer(Modifier.padding(10.dp))

        if (isHideConfigsEnabled) {
            DisableHideConfigsButton(authenticationSettingsViewModel)
        } else {
            when (tryEnableHideConfigsStatus) {
                HideConfigsManager.TryEnableHideConfigsResult.SUCCESS -> {
                    EnableHideConfigsButton(authenticationSettingsViewModel)
                }
                HideConfigsManager.TryEnableHideConfigsResult.ERROR_NO_BIOMETRICS -> {
                    EnableHideConfigsButton(authenticationSettingsViewModel)
                    Text(
                        "Error: no biometrics. Set up biometric authentication on your device and try again.",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                        color = Color.Red
                    )
                }
                HideConfigsManager.TryEnableHideConfigsResult.ERROR_NO_LOCATION -> {
                    EnableHideConfigsButton(authenticationSettingsViewModel)
                    Text(
                        "Error: location permission denied. Grant the permission and try again.",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                        color = Color.Red
                    )
                }
                HideConfigsManager.TryEnableHideConfigsResult.IN_PROGRESS -> {
                    Text(
                        "Please wait...",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                    )
                }
            }
        }
    }
}

@Composable
fun EnableHideConfigsButton(authenticationSettingsViewModel: AuthenticationSettingsViewModel) {
    Button(
        onClick = {
            authenticationSettingsViewModel.tryEnableHideConfigs()
        }
    ) {
        Text("ENABLE")
    }
}

@Composable
fun DisableHideConfigsButton(authenticationSettingsViewModel: AuthenticationSettingsViewModel) {
    Button(
        onClick = {
            authenticationSettingsViewModel.disableHideConfigs()
        }
    ) {
        Text("DISABLE")
    }
}