package com.dobby.feature.main.ui

import com.dobby.feature.diagnostic.domain.VpnConnectionState

data class MainUiState(
    val connectionURL: String = "",
    val connectionState: VpnConnectionState = VpnConnectionState.DISCONNECTED,
    val isVpnStarted: Boolean = false,
)
