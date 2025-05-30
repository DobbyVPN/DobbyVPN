package com.dobby.feature.diagnostic.presentation

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel

class DiagnosticViewModel : ViewModel() {

    private val _uiState: MutableState<UiData> = mutableStateOf(UiData.EMPTY)
    val uiState: State<UiData> = _uiState

    fun reloadIpData() {
        var state by _uiState

        state = UiData("Loading...")

        // TODO
    }
}