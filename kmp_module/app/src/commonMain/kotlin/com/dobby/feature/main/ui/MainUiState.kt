package com.dobby.feature.main.ui

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionProfile
import com.dobby.feature.main.domain.SessionWarning

data class MainUiState(
    val connectionURL: String = "",
    val connectionState: VpnConnectionState = VpnConnectionState.DISCONNECTED,
    val lastFailureCode: SessionFailureCode? = null,
    val lastFailureMessage: String? = null,
    val activeProfile: SessionProfile? = null,
    val warnings: List<SessionWarning> = emptyList(),
)
