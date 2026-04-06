package com.dobby.feature.vpn_service.domain.cloak

sealed interface ConnectResult {
    data object ValidationError : ConnectResult
    data object Success : ConnectResult
    data object AlreadyConnected : ConnectResult
    data class Error(val error: Throwable) : ConnectResult
}

sealed interface DisconnectResult {
    data object Success : DisconnectResult
    data object AlreadyDisconnected : DisconnectResult
    class Error(val error: Throwable) : DisconnectResult
}
