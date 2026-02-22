package com.dobby.feature.vpn_service.domain.cloak

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.feature.vpn_service.DobbyVpnService
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.atomic.AtomicBoolean

class CloakConnectionInteractor(
    private val logger: Logger,
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val cloakLibFacade: CloakLibFacade
) {
    private val isConnected = AtomicBoolean(false)
    private val wasPreviouslyConnected = AtomicBoolean(false)


    suspend fun startCloak(dobbyVpnService: DobbyVpnService?) {
        // If Cloak is enabled, start it BEFORE Outline tries to connect to 127.0.0.1:LocalPort.
        if (dobbyConfigsRepository.getIsCloakEnabled()) {
            val cloakConfig = dobbyConfigsRepository.getCloakConfig()
            val localPort = dobbyConfigsRepository.getCloakLocalPort().toString()
            if (cloakConfig.isNotEmpty()) {
                logger.log("Cloak: connect start")
                val cloakResult = connect(
                    config = cloakConfig,
                    localHost = "127.0.0.1",
                    localPort = localPort
                )
                logger.log("Cloak connection result is $cloakResult")
                if (cloakResult is ConnectResult.Error || cloakResult is ConnectResult.ValidationError) {
                    logger.log("Cloak failed to start, stopping VPN service")
                    dobbyVpnService?.connectionState?.tryUpdateStatus(false)
                    dobbyVpnService?.teardownVpn()
                    dobbyVpnService?.stopSelf()
                    return
                }
            } else {
                logger.log("Cloak is turn on in config, but no config for it was found. Stopping VPN service")
                dobbyVpnService?.connectionState?.tryUpdateStatus(false)
                dobbyVpnService?.teardownVpn()
                dobbyVpnService?.stopSelf()
                return
            }
        }
    }

    fun stopCloak() {

    }

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
                    if (!wasPreviouslyConnected.compareAndSet(false, true)) {
                        cloakLibFacade.stopClient()
                    }
                    cloakLibFacade.startClient(localHost, localPort, config)
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
