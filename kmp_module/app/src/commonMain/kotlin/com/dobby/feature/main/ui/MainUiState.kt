package com.dobby.feature.main.ui

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.main.domain.SessionFailureCode

data class MainUiState(
    val connectionURL: String = "",
    val connectionState: VpnConnectionState = VpnConnectionState.DISCONNECTED,
    val lastFailureCode: SessionFailureCode? = null,
)
