package com.dobby.feature.netcheck.ui

data class NetCheckUiState(
    val netCheckConfig: String = "",
    val netCheckConfigPath: String = "",
    val netCheckStatus: NetCheckStatus = NetCheckStatus.OFF
)
