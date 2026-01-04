package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.presentation.MainViewModel
import kotlinx.coroutines.*
import kotlin.time.TimeMark
import kotlin.time.TimeSource

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

    private val gracePeriodMs: Long = 15_000
    private val consecutiveFailuresBeforeRestart: Int = 3
    private val restartDelayMs: Long = 15_000

    private var consecutiveFailuresCount: Int = 0

    private var restartAttemptsCount: Int = 0
    private val maxRestartAttemptsCount: Int = 3

    private var lastVpnStartMark: TimeMark? = null

    fun startHealthCheck() {
        logger.log("[HC] startHealthCheck() called")

        if (healthJob?.isActive == true) {
            logger.log("[HC] Health check already running → skip start")
            return
        }

        lastVpnStartMark = TimeSource.Monotonic.markNow()

        logger.log("[HC] Health check scheduled (start in ${healthCheck.getTimeToWakeUp()}s)")
        logger.log(
            "[HC] Initial state: consecutiveFailures=$consecutiveFailuresCount, restartAttempts=$restartAttemptsCount"
        )

        healthJob = scope.launch {
            delay(healthCheck.getTimeToWakeUp() * 1_000L)

            logger.log("[HC] Health check started")

            while (isActive) {
                logger.log(
                    "[HC] Tick | consecutiveFailures=$consecutiveFailuresCount/$consecutiveFailuresBeforeRestart | restartAttempts=$restartAttemptsCount/$maxRestartAttemptsCount"
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
                    val sinceStartMs = (lastVpnStartMark?.elapsedNow()?.inWholeMilliseconds)
                        ?: Long.MAX_VALUE
                    if (sinceStartMs < gracePeriodMs) {
                        logger.log("[HC] Not connected during grace period (${sinceStartMs}ms < ${gracePeriodMs}ms) → skip restart")
                        consecutiveFailuresCount = 0
                        delay(10_000)
                        continue
                    }

                    consecutiveFailuresCount++
                    logger.log("[HC] Not connected → consecutiveFailuresCount=$consecutiveFailuresCount/$consecutiveFailuresBeforeRestart")

                    if (consecutiveFailuresCount < consecutiveFailuresBeforeRestart) {
                        delay(10_000)
                        continue
                    }

                    restartAttemptsCount++
                    logger.log("[HC] Failure threshold reached → restartAttemptsCount=$restartAttemptsCount/$maxRestartAttemptsCount")

                    val isUserInitStop = configsRepository.getIsUserInitStop()
                    logger.log("[HC] Cached isUserInitStop=$isUserInitStop before restart")

                    logger.log("[HC] Stopping VPN service (health-check restart)")
                    mainViewModel.stopVpnService(stoppedByHealthCheck = true)
                    logger.log("[HC] stopVpnService() called")

                    if (restartAttemptsCount >= maxRestartAttemptsCount) {
                        logger.log("[HC] restartAttemptsCount limit reached → turning off VPN & stopping health check")
                        turnOffVpn()
                        return@launch
                    }

                    logger.log("[HC] Waiting ${restartDelayMs}ms before restart attempt")
                    delay(restartDelayMs)

                    logger.log("[HC] Restoring isUserInitStop=$isUserInitStop")
                    configsRepository.setIsUserInitStop(isUserInitStop)

                    logger.log("[HC] Starting VPN service (restart)")
                    mainViewModel.startVpnService()
                    lastVpnStartMark = TimeSource.Monotonic.markNow()
                    consecutiveFailuresCount = 0

                    logger.log("[HC] Waiting 3s after restart")
                    delay(3_000)
                } else {
                    consecutiveFailuresCount = 0
                    restartAttemptsCount = 0
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

        restartAttemptsCount = 0
        consecutiveFailuresCount = 0

        logger.log("[HC] State reset after stop")
    }

    suspend fun turnOffVpn() {
        mainViewModel.connectionStateRepository.updateStatus(false)
        mainViewModel.connectionStateRepository.updateVpnStarted(false)
        stopHealthCheck()
        mainViewModel.stopVpnService()
    }
}
