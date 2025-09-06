package com.dobby.feature.vpn_service.domain

import com.dobby.feature.vpn_service.CloakLibFacade
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.atomic.AtomicBoolean

class CloakConnectionInteractor(
    private val cloakLibFacade: CloakLibFacade
) {

    private val isConnected = AtomicBoolean(false)

    private val wasPreviouslyConnected = AtomicBoolean(false)

    suspend fun connect(
        config: String,
        localHost: String = "127.0.0.1",
        localPort: String = "1984",
    ): ConnectResult {
        if (config.isEmpty() || localHost.isEmpty() || localPort.isEmpty()) {
            return ConnectResult.ValidationError
        }
        return withContext(Dispatchers.IO) {
            if (isConnected.compareAndSet(false, true)) {
                val result = runCatching {
                    if (wasPreviouslyConnected.compareAndSet(false, true)) {
                        cloakLibFacade.startClient(localHost, localPort, config)
                    } else {
                        cloakLibFacade.restartClient()
                    }
                }
                if (result.isSuccess) {
                    ConnectResult.Success
                } else {
                    ConnectResult.Error(result.exceptionOrNull()!!)
                }
            } else {
                ConnectResult.AlreadyConnected
            }
        }
    }

    suspend fun disconnect(): DisconnectResult {
        return withContext(Dispatchers.IO) {
            if (isConnected.compareAndSet(true, false)) {
                val result = runCatching { cloakLibFacade.stopClient() }
                if (result.isSuccess) {
                    DisconnectResult.Success
                } else {
                    DisconnectResult.Error(result.exceptionOrNull()!!)
                }
            } else {
                DisconnectResult.AlreadyDisconnected
            }
        }
    }
}
