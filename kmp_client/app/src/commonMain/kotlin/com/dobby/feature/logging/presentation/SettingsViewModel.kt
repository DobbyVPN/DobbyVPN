package com.dobby.feature.logging.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.authentication.presentation.AuthenticationSettingsViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import org.koin.core.component.KoinComponent
import org.koin.core.component.inject

class SettingsViewModel : ViewModel(), KoinComponent {

    private val authenticationSettingsViewModel: AuthenticationSettingsViewModel by inject()

    private val _showBiometricDialog = MutableStateFlow(false)
    val showBiometricDialog = _showBiometricDialog.asStateFlow()

    fun onHideConfigsToggle(checked: Boolean) {
        if (checked) {
            _showBiometricDialog.value = true
        } else {
            authenticationSettingsViewModel.disableHideConfigs()
        }
    }

    fun onDialogConfirm() {
        _showBiometricDialog.value = false
        authenticationSettingsViewModel.tryEnableHideConfigs()
    }

    fun onDialogDismiss() {
        _showBiometricDialog.value = false
    }
}
