package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.presentation.MainViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
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
    private var lastVpnStartMark: TimeMark? = null
    private var lastFullConnectionSucceed = false
    private var healthTickId = 0

    suspend fun startHealthCheck(address: String, port: Int) {
        logger.log("[HC] startHealthCheck() called")

        if (healthJob?.isActive == true) {
            logger.log("[HC] Health check already running → skip start")
            return
        }

        lastVpnStartMark = TimeSource.Monotonic.markNow()

        logger.log("[HC] Health check scheduled (start in ${healthCheck.getTimeToWakeUp()}s)")
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
                val tickId = ++healthTickId
                logger.log(
                    "[HC] tick#$tickId begin " +
                        "lastFullConnectionSucceed=$lastFullConnectionSucceed"
                )

                if (configsRepository.getIsUserInitStop()) {
                    logger.log(
                        "[HC] tick#$tickId stop condition: " +
                            "getIsUserInitStop() == true → exiting health check loop"
                    )
                    resetHealthCheckState()
                    return@launch
                }

                if (mainViewModel.connectionStateRepository.vpnTransitioningFlow.value) {
                    logger.log("[HC] tick#$tickId VPN is transitioning → skipping checks for this tick")
                    nextDelay = getHealthCheckDelay()
                    logger.log("[HC] Next tick in $nextDelay")
                    delay(nextDelay)
                    continue
                }

                val connected = try {
                    val result = isConnected(tickId)
                    logger.log("[HC] tick#$tickId isConnected() result = $result")
                    result
                } catch (cancelled: CancellationException) {
                    logger.log("[HC] tick#$tickId cancelled while health checks were running")
                    throw cancelled
                } catch (t: Throwable) {
                    logger.log("[HC] tick#$tickId isConnected() threw exception: ${t.message}")
                    false
                }

                if (!isActive) {
                    logger.log("[HC] tick#$tickId result ignored because health job was cancelled")
                    return@launch
                }

                val vpnStarted = mainViewModel.connectionStateRepository.vpnStartedFlow.value
                if (!vpnStarted) {
                    logger.log("[HC] tick#$tickId vpnStarted=false → exiting health check loop")
                    resetHealthCheckState()
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
                        logger.log(
                            "[HC] Not connected during grace period " +
                                "(${sinceStartMs}ms < ${gracePeriodMs}ms) → ignore"
                        )
                        nextDelay = getHealthCheckDelay()
                    }

                    if (nextDelay == null) {
                        logger.log("[HC] Not connected → continuing health check loop")
                        nextDelay = getHealthCheckDelay()
                    }
                } else {
                    logger.log("[HC] OK")

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

        resetHealthCheckState()

        logger.log("[HC] State reset after stop")
    }

    private fun resetHealthCheckState() {
        lastVpnStartMark = null
        lastFullConnectionSucceed = false
        healthTickId = 0
    }

    suspend fun turnOffVpn() {
        mainViewModel.connectionStateRepository.updateStatus(false)
        mainViewModel.connectionStateRepository.updateVpnStarted(false)
        stopHealthCheck()
        mainViewModel.stopVpnService()
    }

    private suspend fun isConnected(tickId: Int): Boolean {
        currentCoroutineContext().ensureActive()
        var result = false
        if (lastFullConnectionSucceed) {
            logger.log("[HC] tick#$tickId using shortConnectionCheckUp() first")
            result = healthCheck.shortConnectionCheckUp()
            currentCoroutineContext().ensureActive()
        }
        if (!result) {
            currentCoroutineContext().ensureActive()
            logger.log("[HC] tick#$tickId using fullConnectionCheckUp()")
            result = healthCheck.fullConnectionCheckUp()
            currentCoroutineContext().ensureActive()
            lastFullConnectionSucceed = result
            logger.log("[HC] tick#$tickId fullConnectionCheckUp() result=$result")
        }
        return result
    }

    private fun getHealthCheckDelay(): Duration {
        val mark = lastVpnStartMark ?: return 2.seconds
        val elapsed = mark.elapsedNow()

        if (lastFullConnectionSucceed) {
            return when {
                elapsed < 30.seconds -> 5.seconds
                elapsed < 90.seconds -> 10.seconds
                else -> 15.seconds
            }
        }

        return when {
            elapsed < 30.seconds -> 2.seconds
            elapsed < 90.seconds -> 5.seconds
            else -> 10.seconds
        }
    }
}
