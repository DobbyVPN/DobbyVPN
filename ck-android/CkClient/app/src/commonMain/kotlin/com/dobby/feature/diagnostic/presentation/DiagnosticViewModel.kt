package com.dobby.feature.diagnostic.presentation

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import com.dobby.feature.diagnostic.domain.IpRepository

class DiagnosticViewModel(
    private val ipRepository: IpRepository,
) : ViewModel() {

    private val _uiState: MutableState<UiData> = mutableStateOf(UiData.EMPTY)
    val uiState: State<UiData> = _uiState

    suspend fun reloadIpData() {
        var state by _uiState

        state = UiData.LOADING
        val data = ipRepository.getIpData()
        state = UiData(data.ip, data.city, data.country)
    }
}