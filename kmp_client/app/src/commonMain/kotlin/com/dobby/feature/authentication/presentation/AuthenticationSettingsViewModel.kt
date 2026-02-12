package com.dobby.feature.authentication.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.authentication.domain.HideConfigsManager
import com.dobby.feature.authentication.domain.HideConfigsManager.isHideConfigsEnabled
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

class AuthenticationSettingsViewModel(): ViewModel() {
    private val scope = MainScope()

    private val _hideConfigsSettingState = MutableStateFlow(false).also {
        scope.launch {
            it.emit(isHideConfigsEnabled())
        }
    }
    val hideConfigsSettingState: StateFlow<Boolean> = _hideConfigsSettingState

    private val _tryEnableHideConfigsStatus = MutableStateFlow(HideConfigsManager.TryEnableHideConfigsResult.SUCCESS)
    val tryEnableHideConfigsStatus: StateFlow<HideConfigsManager.TryEnableHideConfigsResult> = _tryEnableHideConfigsStatus

    fun tryEnableHideConfigs() {
        scope.launch {
            _tryEnableHideConfigsStatus.emit(HideConfigsManager.TryEnableHideConfigsResult.IN_PROGRESS)
            HideConfigsManager.tryEnableHideConfigs { res ->
                scope.launch {
                    if (res == HideConfigsManager.TryEnableHideConfigsResult.SUCCESS) {
                        _hideConfigsSettingState.emit(true)
                    }

                    _tryEnableHideConfigsStatus.emit(res)
                    HideConfigsManager.authStatus = HideConfigsManager.AuthStatus.SUCCESS
                }
            }
        }
    }

    fun disableHideConfigs() {
        HideConfigsManager.disableHideConfigs()
        scope.launch {
            _hideConfigsSettingState.emit(false)
        }
    }
}

