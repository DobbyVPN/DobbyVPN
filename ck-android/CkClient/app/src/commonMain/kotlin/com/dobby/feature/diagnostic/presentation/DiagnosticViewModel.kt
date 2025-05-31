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

        state = UiData(IpData.LOADING, state.dnsData)
        val data = ipRepository.getIpData()
        state = UiData(IpData(data.ip, data.city, data.country), state.dnsData)
    }

    suspend fun reloadDnsIpData(hostname: String) {
        var state by _uiState

        state = UiData(state.ipData, IpData.LOADING)
        val data = ipRepository.getHostnameIpData(hostname)
        state = UiData(state.ipData, IpData(data.ip, data.city, data.country))
    }
}