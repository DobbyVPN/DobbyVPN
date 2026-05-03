package com.dobby.feature.netcheck.ui

data class NetCheckUiState(
    val tomlConfig: String = "",
    val netCheckStatus: NetCheckStatus = NetCheckStatus.OFF,
    val description: String = ""
)
