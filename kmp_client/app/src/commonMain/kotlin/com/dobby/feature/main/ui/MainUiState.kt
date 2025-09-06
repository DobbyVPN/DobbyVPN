package com.dobby.feature.main.ui

data class MainUiState(
    val cloakJson: String = "",
    val outlineKey: String = "",
    val isConnected: Boolean = false,
    val isCloakEnabled: Boolean = false,
)
