package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.presentation.MainViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds
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
    private val consecutiveFailuresBeforeTurnOff: Int = 2
    private val restartDelayMs: Long = 15_000

    private var consecutiveFailuresCount: Int = 0

    private var lastVpnStartMark: TimeMark? = null

    private var healthCheckStartMark: TimeMark? = null

    fun onUserManualStartRequested() {
        mainViewModel.connectionStateRepository.tryUpdateRestartPending(false)
        logger.log("[HC] User requested manual start → restartPending=false")
    }

    fun startHealthCheck() {
        logger.log("[HC] startHealthCheck() called")

        if (healthJob?.isActive == true) {
            logger.log("[HC] Health check already running → skip start")
            return
        }

        lastVpnStartMark = TimeSource.Monotonic.markNow()

        logger.log("[HC] Health check scheduled (start in ${healthCheck.getTimeToWakeUp()}s)")
        logger.log(
            "[HC] Initial state: consecutiveFailures=$consecutiveFailuresCount"
        )

        healthCheckStartMark = TimeSource.Monotonic.markNow()

        healthJob = scope.launch {
            delay(healthCheck.getTimeToWakeUp() * 1_000L)

            logger.log("[HC] Health check started")

            while (isActive) {
                val vpnStarted = mainViewModel.connectionStateRepository.vpnStartedFlow.value
                val restartPending = mainViewModel.connectionStateRepository.restartPendingFlow.value
                val isUserInitStopNow = configsRepository.getIsUserInitStop()
                logger.log(
                    "[HC] Tick | consecutiveFailures=$consecutiveFailuresCount/$consecutiveFailuresBeforeTurnOff | vpnStarted=$vpnStarted restartPending=$restartPending isUserInitStop=$isUserInitStopNow"
                )

                var nextDelay: Duration? = null

                if (isUserInitStopNow) {
                    logger.log("[HC] Stop condition: getIsUserInitStop() == true")
                    turnOffVpn()
                    return@launch
                }

                if (!vpnStarted && !restartPending) {
                    logger.log("[HC] vpnStarted=false and restartPending=false → exiting health check loop")
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

                if (!isActive) return@launch

                val vpnStartedNow = mainViewModel.connectionStateRepository.vpnStartedFlow.value
                val restartPendingNow = mainViewModel.connectionStateRepository.restartPendingFlow.value
                if (!vpnStartedNow && !restartPendingNow) {
                    logger.log("[HC] Stop observed after check → exit without applying results")
                    return@launch
                }

                if (connected && vpnStartedNow && !restartPendingNow) {
                    mainViewModel.connectionStateRepository.updateStatus(true)
                }

                if (!connected) {
                    mainViewModel.connectionStateRepository.updateStatus(false)

                    val sinceStartMs = (lastVpnStartMark?.elapsedNow()?.inWholeMilliseconds)
                        ?: Long.MAX_VALUE
                    if (sinceStartMs < gracePeriodMs) {
                        logger.log("[HC] Not connected during grace period (${sinceStartMs}ms < ${gracePeriodMs}ms) → ignore")
                        nextDelay = getHealthCheckDelay()
                    }

                    if (nextDelay == null) {
                        consecutiveFailuresCount++
                        logger.log("[HC] Not connected → consecutiveFailuresCount=$consecutiveFailuresCount/$consecutiveFailuresBeforeTurnOff")

                        if (consecutiveFailuresCount >= consecutiveFailuresBeforeTurnOff) {
                            logger.log("[HC] Failure threshold reached → turning off VPN & stopping health check")
                            turnOffVpn()
                            return@launch
                        }

                        val isUserInitStop = configsRepository.getIsUserInitStop()
                        logger.log("[HC] Cached isUserInitStop=$isUserInitStop before restart")

                        logger.log("[HC] Stopping VPN service (health-check restart)")
                        mainViewModel.connectionStateRepository.updateVpnStarted(false)
                        mainViewModel.stopVpnService(stoppedByHealthCheck = true)
                        logger.log("[HC] stopVpnService() called")

                        logger.log("[HC] Waiting ${restartDelayMs}ms before restart attempt")
                        mainViewModel.connectionStateRepository.tryUpdateRestartPending(true)
                        delay(restartDelayMs)

                        val restartPendingNow = mainViewModel.connectionStateRepository.restartPendingFlow.value
                        val vpnStartedNow = mainViewModel.connectionStateRepository.vpnStartedFlow.value
                        if (!restartPendingNow || vpnStartedNow) {
                            logger.log("[HC] Auto-restart cancelled/invalid → skip restart (restartPending=$restartPendingNow vpnStarted=$vpnStartedNow)")
                            mainViewModel.connectionStateRepository.tryUpdateRestartPending(false)
                            nextDelay = getHealthCheckDelay()
                        } else {
                            logger.log("[HC] Restoring isUserInitStop=$isUserInitStop")
                            configsRepository.setIsUserInitStop(isUserInitStop)

                            logger.log("[HC] Starting VPN service (restart)")
                            mainViewModel.connectionStateRepository.updateVpnStarted(true)
                            mainViewModel.connectionStateRepository.tryUpdateRestartPending(false)
                            mainViewModel.startVpnService()

                            lastVpnStartMark = TimeSource.Monotonic.markNow()
                            healthCheckStartMark = TimeSource.Monotonic.markNow()

                            logger.log("[HC] Waiting 3s after restart")
                            nextDelay = 3.seconds
                        }
                    }
                } else {
                    logger.log("[HC] OK")
                    consecutiveFailuresCount = 0
                    logger.log("[HC] Connected → counters reset")

                    nextDelay = getHealthCheckDelay()
                }

                val delayDuration = nextDelay ?: getHealthCheckDelay()
                logger.log("[HC] Next tick in $delayDuration")
                delay(delayDuration)
            }

            logger.log("[HC] Health check loop finished (job inactive)")
        }
    }

    fun stopHealthCheck() {
        logger.log("[HC] stopHealthCheck() called")
        logger.log("[HC] Cancelling job: ${healthJob?.isActive == true}")

        healthJob?.cancel()
        healthJob = null

        consecutiveFailuresCount = 0
        lastVpnStartMark = null
        healthCheckStartMark = null
        mainViewModel.connectionStateRepository.tryUpdateRestartPending(false)

        logger.log("[HC] State reset after stop")
    }

    suspend fun turnOffVpn() {
        mainViewModel.connectionStateRepository.updateStatus(false)
        mainViewModel.connectionStateRepository.updateVpnStarted(false)
        stopHealthCheck()
        mainViewModel.stopVpnService()
    }

    private fun getHealthCheckDelay(): Duration {
        val mark = healthCheckStartMark ?: return 2.seconds
        val elapsed = mark.elapsedNow()

        return when {
            elapsed < 30.seconds -> 2.seconds
            elapsed < 90.seconds -> 5.seconds
            else -> 10.seconds
        }
    }
}
