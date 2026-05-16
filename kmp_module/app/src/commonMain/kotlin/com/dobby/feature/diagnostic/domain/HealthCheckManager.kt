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
    private var lastVpnStartMark: TimeMark? = null
    private var lastFullConnectionSucceed = false
    private var healthGeneration = 0L

    suspend fun startHealthCheck(address: String, port: Int) {
        logger.log("[HC] startHealthCheck() called")

        if (healthJob?.isActive == true) {
            logger.log("[HC] Health check already running → skip start")
            return
        }

        lastVpnStartMark = TimeSource.Monotonic.markNow()

        logger.log("[HC] Health check scheduled (start in ${healthCheck.getTimeToWakeUp()}s)")
        logger.log(
            "[HC] Initial state: consecutiveFailuresCount=0"
        )

        logger.log("[HC] Health check started")
        healthGeneration += 1
        val generation = healthGeneration

        if (address != "localhost" && address != "127.0.0.1") {
            val serverAlive = healthCheck.checkServerAlive(address, port)
            if (!serverAlive) {
                logger.log("[HC] Server isn't alive at start, continuing into check loop anyway")
            } else {
                logger.log("[HC] Server is alive")
            }
        }

        healthJob = scope.launch {
            delay(healthCheck.getTimeToWakeUp() * 1_000L)

            while (isActive) {
                if (generation != healthGeneration) {
                    logger.log("[HC] Stop condition: generation changed → exiting loop")
                    return@launch
                }

                if (configsRepository.getIsUserInitStop()) {
                    logger.log("[HC] Stop condition: getIsUserInitStop() == true → exiting loop")
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

                if (generation != healthGeneration) {
                    logger.log("[HC] Health check stopped while native check was in flight → ignoring result")
                    return@launch
                }

                val vpnStarted = mainViewModel.connectionStateRepository.vpnStartedFlow.value
                if (!vpnStarted) {
                    logger.log("[HC] vpnStarted=false → exiting health check loop")
                    return@launch
                }

                if (connected) {
                    mainViewModel.connectionStateRepository.updateStatus(true)
                    logger.log("[HC] OK")
                    logger.log("[HC] Connected → counters reset")
                } else {
                    mainViewModel.connectionStateRepository.updateStatus(false)

                    val sinceStartMs = (lastVpnStartMark?.elapsedNow()?.inWholeMilliseconds)
                        ?: Long.MAX_VALUE
                    if (sinceStartMs < gracePeriodMs) {
                        logger.log("[HC] Not connected during grace period (${sinceStartMs}ms < ${gracePeriodMs}ms) → ignore")
                    } else {
                        logger.log("[HC] Not connected — will retry on next tick")
                    }
                }

                val nextDelay = getHealthCheckDelay()
                logger.log("[HC] Next tick in $nextDelay")
                delay(nextDelay)
            }

            logger.log("[HC] Health check loop finished (job inactive)")
        }
    }

    fun stopHealthCheck() {
        logger.log("[HC] stopHealthCheck() called")
        logger.log("[HC] Cancelling job: ${healthJob?.isActive == true}")
        logger.log("[HC] In-flight native checks may continue logging until their timeout")

        healthGeneration += 1
        healthJob?.cancel()
        healthJob = null

        lastVpnStartMark = null
        lastFullConnectionSucceed = false

        logger.log("[HC] State reset after stop")
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
