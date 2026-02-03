package com.dobby.feature.logging.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withLink
import androidx.compose.ui.unit.dp
import com.dobby.feature.authentication.domain.HideConfigsManager
import com.dobby.feature.authentication.presentation.AuthenticationSettingsViewModel
import com.dobby.feature.logging.presentation.SettingsViewModel
import com.dobby.vpn.BuildConfig
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

@Composable
fun SettingsScreen(
    modifier: Modifier = Modifier,
    authenticationSettingsViewModel: AuthenticationSettingsViewModel,
    settingsViewModel: SettingsViewModel
) {
    val showBiometricDialog by settingsViewModel.showBiometricDialog.collectAsState()

    val isHideConfigsEnabled by authenticationSettingsViewModel.hideConfigsSettingState.collectAsState()
    val tryEnableHideConfigsStatus by authenticationSettingsViewModel.tryEnableHideConfigsStatus.collectAsState()

    Column(modifier = modifier) {
        Text(
            text = "DobbyVPN",
            fontSize = MaterialTheme.typography.headlineMedium.fontSize,
            maxLines = 1,
            modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 0.dp, bottom = 16.dp)
        )
        Column(
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            AboutRow("Version:", BuildConfig.VERSION_NAME)
            AboutRowLink(
                title = "Build commit:",
                value = BuildConfig.PROJECT_REPOSITORY_COMMIT,
                link = BuildConfig.PROJECT_REPOSITORY_COMMIT_LINK,
            )
            Spacer(Modifier.padding(4.dp))
            Box(
                modifier = Modifier
                    .padding(horizontal = 8.dp)
                    .fillMaxWidth()
                    .border(BorderStroke(2.dp, MaterialTheme.colorScheme.outline))
            ) {
                Row(
                    modifier = Modifier
                        .padding(12.dp)
                        .fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        text = "Hide configurations",
                        fontWeight = FontWeight.Bold,
                    )
                    Switch(
                        checked = isHideConfigsEnabled,
                        onCheckedChange = { checked ->
                            settingsViewModel.onHideConfigsToggle(checked)
                        },
                    )
                }
            }

            if (showBiometricDialog) {
                BiometricPermissionDialog(
                    onAccept = { settingsViewModel.onDialogConfirm() },
                    onDecline = { settingsViewModel.onDialogDismiss() }
                )
            }

            when (tryEnableHideConfigsStatus) {
                HideConfigsManager.TryEnableHideConfigsResult.SUCCESS -> {}
                HideConfigsManager.TryEnableHideConfigsResult.ERROR_NO_BIOMETRICS -> {
                    Text(
                        "Error: no biometrics. Set up biometric authentication on your device and try again.",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                        color = Color.Red,
                        modifier = Modifier.padding(start = 6.dp, end = 6.dp)
                    )
                }
                HideConfigsManager.TryEnableHideConfigsResult.ERROR_NO_LOCATION -> {
                    Text(
                        "Error: location permission denied. Grant the permission and try again.",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                        color = Color.Red,
                        modifier = Modifier.padding(start = 6.dp, end = 6.dp)
                    )
                }
                HideConfigsManager.TryEnableHideConfigsResult.IN_PROGRESS -> {
                    Text(
                        "Please wait...",
                        fontSize = MaterialTheme.typography.bodyMedium.fontSize,
                        fontWeight = FontWeight.Normal,
                        modifier = Modifier.padding(start = 6.dp, end = 6.dp)
                    )
                }
            }
        }
    }
}

@Composable
fun AboutRow(
    title: String,
    value: String,
    modifier: Modifier = Modifier
) {
    Row(
        modifier = modifier.padding(horizontal = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        Text(
            text = title,
            fontWeight = FontWeight.Bold,
        )
        Text(
            text = value,
            fontWeight = FontWeight.Normal,
        )
    }
}

@Composable
fun AboutRowLink(
    title: String,
    value: String,
    link: String,
    modifier: Modifier = Modifier
) {
    Row(
        modifier = modifier.padding(horizontal = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        Text(
            text = title,
            fontWeight = FontWeight.Bold,
        )

        Text(
            buildAnnotatedString {
                withLink(
                    LinkAnnotation.Url(
                        url = link,
                        styles = TextLinkStyles(
                            style = SpanStyle(color = Color.Blue),
                        )
                    )
                ) {
                    append(value)
                }
            }
        )
    }
}
