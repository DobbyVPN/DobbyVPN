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

    private var lastFullConnectionSucceed = false

    suspend fun startHealthCheck(address: String, port: Int) {
        logger.log("[HC] startHealthCheck() called")

        if (healthJob?.isActive == true) {
            logger.log("[HC] Health check already running → skip start")
            return
        }

        lastVpnStartMark = TimeSource.Monotonic.markNow()

        logger.log("[HC] Health check scheduled (start in ${healthCheck.getTimeToWakeUp()}s)")
        logger.log(
            "[HC] Initial state: consecutiveFailuresCount=$consecutiveFailuresCount"
        )

        logger.log("[HC] Health check started")

        if (address != "localhost" && address != "127.0.0.1") {
            val serverAlive = healthCheck.checkServerAlive(address, port)
            if (!serverAlive) {
                logger.log("[HC] Server isn't alive")
                turnOffVpn()
                return
            }
            logger.log("[HC] Server is alive")

        }


        healthJob = scope.launch {
            delay(healthCheck.getTimeToWakeUp() * 1_000L)

            while (isActive) {
                var nextDelay: Duration? = null

                if (configsRepository.getIsUserInitStop()) {
                    logger.log("[HC] Stop condition: getIsUserInitStop() == true")
                    turnOffVpn()
                    return@launch
                }

                val connected = try {
                    val result = isConnected()
                    logger.log("[HC] isConnected() result = $result")
                    result
                } catch (t: Throwable) {
                    logger.log("[HC] isConnected() threw exception: ${t.message}")
                    false
                }

                var vpnStarted = mainViewModel.connectionStateRepository.vpnStartedFlow.value
                if (!vpnStarted) {
                    logger.log("[HC] vpnStarted=false → exiting health check loop")
                    return@launch
                }

                if (connected && vpnStarted) {
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
                        mainViewModel.stopVpnService(stoppedByHealthCheck = true)

                        logger.log("[HC] Waiting ${restartDelayMs}ms before restart attempt")
                        delay(restartDelayMs)

                        logger.log("[HC] Restoring isUserInitStop=$isUserInitStop")
                        configsRepository.setIsUserInitStop(isUserInitStop)

                        logger.log("[HC] Starting VPN service (restart)")
                        mainViewModel.startVpnService()

                        lastVpnStartMark = TimeSource.Monotonic.markNow()

                        logger.log("[HC] Waiting 3s after restart")
                        nextDelay = 3.seconds
                    }
                } else {
                    logger.log("[HC] OK")
                    consecutiveFailuresCount = 0
                    logger.log("[HC] Connected → counters reset")

                    nextDelay = getHealthCheckDelay()
                }

                logger.log("[HC] Next tick in $nextDelay")
                delay(nextDelay)
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

        lastFullConnectionSucceed = false

        logger.log("[HC] State reset after stop")
    }

    suspend fun turnOffVpn() {
        mainViewModel.connectionStateRepository.updateStatus(false)
        mainViewModel.connectionStateRepository.updateVpnStarted(false)
        stopHealthCheck()
        mainViewModel.stopVpnService()
    }

    private fun isConnected(): Boolean {
        var result = false
        if (lastFullConnectionSucceed) {
            result = healthCheck.shortConnectionCheckUp()
        }
        if (!result) {
            result = healthCheck.fullConnectionCheckUp()
            lastFullConnectionSucceed = result
        }
        return result
    }

    private fun getHealthCheckDelay(): Duration {
        val mark = lastVpnStartMark ?: return 2.seconds
        val elapsed = mark.elapsedNow()

        return when {
            elapsed < 30.seconds -> 2.seconds
            elapsed < 90.seconds -> 5.seconds
            else -> 10.seconds
        }
    }
}
