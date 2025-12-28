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

    private val scope = CoroutineScope(
        SupervisorJob() + Dispatchers.Default.limitedParallelism(1)
    )
    private var healthJob: Job? = null

    private var startAttemptsCount: Int = 0
    private val maxStartAttemptsCount: Int = 2

    private var retriesCount: Int = 0
    private val maxRetriesCount: Int = 3

    fun startHealthCheck() {
        logger.log("[HC] startHealthCheck() called")

        if (healthJob?.isActive == true) {
            logger.log("[HC] Health check already running → skip start")
            return
        }

        logger.log("[HC] Health check scheduled (start in 5s)")
        logger.log("[HC] Initial state: startAttempts=$startAttemptsCount, retries=$retriesCount")

        healthJob = scope.launch {
            delay(healthCheck.getTimeToWakeUp() * 1_000L)

            logger.log("[HC] Health check started")

            while (isActive) {
                logger.log(
                    "[HC] Tick | startAttempts=$startAttemptsCount/$maxStartAttemptsCount | retries=$retriesCount/$maxRetriesCount"
                )

                if (configsRepository.getIsUserInitStop()) {
                    logger.log("[HC] Stop condition: getIsUserInitStop() == true")
                    turnOffVpn()
                    return@launch
                }

                val connected = try {
                    logger.log("[HC] Calling healthCheck.isConnected()")
                    val result = healthCheck.isConnected()
                    logger.log("[HC] isConnected() result = $result")
                    result
                } catch (t: Throwable) {
                    logger.log("[HC] isConnected() threw exception: ${t.message}")
                    false
                }

                logger.log("[HC] Updating connection state to: $connected")
                mainViewModel.connectionStateRepository.updateStatus(connected)

                if (!connected) {
                    startAttemptsCount++
                    logger.log("[HC] Not connected → startAttemptsCount=$startAttemptsCount")

                    if (startAttemptsCount >= maxStartAttemptsCount) {
                        logger.log("[HC] startAttemptsCount limit reached → stopping health check")
                        turnOffVpn()
                        return@launch
                    }

                    retriesCount++
                    logger.log("[HC] Runtime failure → retriesCount=$retriesCount/$maxRetriesCount")
                    logger.log("[HC] VPN considered dead → stopping VPN")

                    val isUserInitStop = configsRepository.getIsUserInitStop()
                    logger.log("[HC] Cached isUserInitStop=$isUserInitStop before stop")

                    mainViewModel.stopVpnService(stoppedByHealthCheck = true)
                    logger.log("[HC] stopVpnService() called")

                    if (retriesCount >= maxRetriesCount) {
                        logger.log("[HC] retriesCount limit reached → stopping health check")
                        turnOffVpn()
                        return@launch
                    }

                    logger.log("[HC] Waiting 15s before restart attempt")
                    delay(15_000)

                    logger.log("[HC] Restoring isUserInitStop=$isUserInitStop")
                    configsRepository.setIsUserInitStop(isUserInitStop)

                    logger.log("[HC] Starting VPN service")
                    mainViewModel.startVpnService()

                    logger.log("[HC] initStart reset to true, waiting 3s")
                    delay(3_000)

                } else {
                    startAttemptsCount = 0
                    logger.log("[HC] Connected → counters reset")
                }

                delay(10_000)
            }

            logger.log("[HC] Health check loop finished (job inactive)")
        }
    }

    fun stopHealthCheck() {
        logger.log("[HC] stopHealthCheck() called")
        logger.log("[HC] Cancelling job: ${healthJob?.isActive == true}")

        healthJob?.cancel()
        healthJob = null

        retriesCount = 0
        startAttemptsCount = 0

        logger.log("[HC] State reset after stop")
    }

    suspend fun turnOffVpn() {
        mainViewModel.connectionStateRepository.updateStatus(false)
        mainViewModel.connectionStateRepository.updateVpnStarted(false)
        stopHealthCheck()
        mainViewModel.stopVpnService()
    }
}
