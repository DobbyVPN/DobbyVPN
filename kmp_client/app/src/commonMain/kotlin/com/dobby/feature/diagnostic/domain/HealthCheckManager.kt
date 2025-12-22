package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.presentation.MainViewModel
import kotlinx.coroutines.*

class HealthCheckManager(
    private val healthCheck: HealthCheck,
    private val mainViewModel: MainViewModel,
    private val configsRepository: DobbyConfigsRepository,
    private val logger: Logger,
) {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var healthJob: Job? = null

    private var initStart = true

    private var retriesCount: Int = 0
    private val maxRetriesCount: Int = 3

    fun startHealthCheck() {
        if (healthJob?.isActive == true) {
            logger.log("Health check already running")
            return
        }

        logger.log("Health check scheduled (start in 5s)")

        healthJob = scope.launch {
            delay(5_000)

            logger.log("Health check started")

            while (isActive) {
                if (configsRepository.getIsUserInitStop()) {
                    logger.log("Health check stopped: because getIsUserInitStop() return true")
                    stopHealthCheck()
                    return@launch
                }

                val connected = try {
                    healthCheck.isConnected()
                } catch (t: Throwable) {
                    logger.log("Health check error: ${t.message}")
                    false
                }

                mainViewModel.connectionStateRepository.update(connected)

                if (!connected) {
                    if (initStart) {
                        logger.log("First fail: could be uninitialized VPN, sleep 5s")
                        delay(5_000)
                        continue
                    }
                    retriesCount++
                    logger.log("Health check failed ($retriesCount/$maxRetriesCount)")
                    logger.log("Health check: VPN considered dead → stopping VPN")
                    val isUserInitStop = configsRepository.getIsUserInitStop()
                    mainViewModel.stopVpnService()

                    if (retriesCount >= maxRetriesCount) {
                        stopHealthCheck()
                        return@launch
                    }

                    delay(15_000)
                    configsRepository.setIsUserInitStop(isUserInitStop)

                    mainViewModel.startVpnService()
                    initStart = true
                    delay(3_000)
                } else {
                    initStart = false
                }

                delay(2_000)
            }
        }
    }

    fun stopHealthCheck() {
        logger.log("Health check stopped")
        healthJob?.cancel()
        healthJob = null
        retriesCount = 0
    }
}
