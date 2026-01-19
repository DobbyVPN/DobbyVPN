package com.dobby.feature.diagnostic.domain

import com.dobby.feature.logging.Logger
import interop.VPNLibraryLoader
import java.net.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread
import kotlin.system.measureTimeMillis

class HealthCheckImpl(
    private val logger: Logger,
    private val vpnLibrary: VPNLibraryLoader,
) : HealthCheck {

    private val timeoutMs = 1_000L

    @Volatile
    var currentMemoryUsageMb: Double = -1.0
        private set

    override fun shortConnectionCheckUp(): Boolean {
        logger.log("Start shortConnectionCheckUp")

        val checks: List<Pair<String, () -> Boolean>> = listOf(
            "HTTP https://google.com/gen_204" to {
                httpPing("https://google.com/gen_204")
            },
            "HTTP https://1.1.1.1" to {
                httpPing("https://1.1.1.1")
            }
        )

        val networkOk = checks.any { (name, check) ->
            runWithRetry(name = name, attempts = 2, block = check)
        }

        logger.log("End shortConnectionCheckUp => $networkOk")
        return networkOk
    }


    override fun fullConnectionCheckUp(): Boolean {
        logger.log("[HealthCheck] START")
        logger.log("Start fullConnectionCheckUp")

        val groups: List<Pair<String, List<Pair<String, () -> Boolean>>>> = listOf(

            // --- Group 1: TCP ping to public DNS servers ---
            "TCP Ping group" to listOf(
                "Ping 8.8.8.8" to { pingAddress("8.8.8.8", 53, "Google") },
                "Ping 1.1.1.1" to { pingAddress("1.1.1.1", 53, "OneOneOneOne") }
            ),

            // --- Group 2: DNS resolve ---
            "DNS Resolve group" to listOf(
                "DNS google.com" to { resolveDnsWithTimeout("google.com") != null },
                "DNS one.one.one.one" to { resolveDnsWithTimeout("one.one.one.one") != null }
            ),

            // --- Group 3: DNS-based TCP ping ---
            "DNS Ping group" to listOf(
                "Ping google.com (DNS)" to { pingAddress("google.com", 80, "GoogleDNS") },
                "Ping one.one.one.one (DNS)" to { pingAddress("one.one.one.one", 80, "OnesDNS") }
            )
        )

        var result = true

        for ((groupName, checks) in groups) {
            logger.log("[HealthCheck] Checking group: $groupName")

            val groupOk = checks.any { (name, check) ->
                runWithRetry(name = name, attempts = 2, block = check)
            }

            if (!groupOk) {
                logger.log("[HealthCheck] Group FAILED: $groupName")
                result = false
            } else {
                logger.log("[HealthCheck] Group OK: $groupName")
            }
        }

        if (!shortConnectionCheckUp()) {
            logger.log("[HealthCheck] shortConnectionCheckUp FAILED inside full check")
            result = false
        }

        currentMemoryUsageMb = getProcessMemoryUsageMb()

        if (currentMemoryUsageMb >= 0) {
            logger.log(
                "[HealthCheck] Memory usage: %.2f MB".format(currentMemoryUsageMb)
            )
        } else {
            logger.log("[HealthCheck] Memory usage: unknown")
            result = false
        }

        logger.log("[HealthCheck] RESULT = $result")
        return result
    }


    private fun runWithRetry(
        name: String,
        attempts: Int,
        block: () -> Boolean
    ): Boolean {
        repeat(attempts) { attempt ->
            logger.log("[HealthCheck] $name attempt ${attempt + 1}")
            if (block()) return true
        }
        logger.log("[HealthCheck] $name FAILED after $attempts attempts")
        return false
    }

    private fun resolveDnsWithTimeout(host: String): String? {
        val latch = CountDownLatch(1)
        var result: String? = null

        thread(name = "dns-resolve") {
            try {
                val addresses = InetAddress.getAllByName(host)
                result = addresses.firstOrNull()?.hostAddress
            } catch (_: Throwable) {
            } finally {
                latch.countDown()
            }
        }

        return if (latch.await(timeoutMs, TimeUnit.MILLISECONDS)) {
            result
        } else {
            null
        }
    }

    private fun httpPing(urlString: String): Boolean {
        return try {
            val url = URL(urlString)
            val conn = (url.openConnection() as HttpURLConnection).apply {
                requestMethod = "GET"
                connectTimeout = timeoutMs.toInt()
                readTimeout = timeoutMs.toInt()
                useCaches = false
            }
            conn.connect()
            conn.responseCode in 200..399
        } catch (_: Throwable) {
            false
        }
    }

    private fun pingAddress(
        host: String,
        port: Int,
        name: String
    ): Boolean {
        var success = false
        val elapsedMs = measureTimeMillis {
            try {
                Socket().use { socket ->
                    socket.connect(
                        InetSocketAddress(host, port),
                        timeoutMs.toInt()
                    )
                }
                success = true
            } catch (_: Throwable) {
            }
        }

        if (success) {
            logger.log("[ping $name] $elapsedMs ms")
        } else {
            logger.log("[ping $name] error")
        }

        return success
    }

    private fun getProcessMemoryUsageMb(): Double {
        val runtime = Runtime.getRuntime()
        val usedBytes = runtime.totalMemory() - runtime.freeMemory()
        return usedBytes / 1024.0 / 1024.0
    }

    override fun checkServerAlive(address: String, port: Int): Boolean {
        return vpnLibrary.checkServerAlive(address, port)
    }

    override fun getTimeToWakeUp(): Int {
        return 15
    }
}
