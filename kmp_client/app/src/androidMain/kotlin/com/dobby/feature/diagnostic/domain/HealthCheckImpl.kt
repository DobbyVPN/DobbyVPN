package com.dobby.feature.diagnostic.domain

import android.os.SystemClock
import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.DobbyVpnService
import java.net.*
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

class HealthCheckImpl(
    private val logger: Logger,
) : HealthCheck {

    override val timeToWakeUp = 2

    private val timeoutMs = 1_000L

    @Volatile
    var currentMemoryUsageMb: Double = -1.0
        private set

    override fun isConnected(): Boolean {
        logger.log("[HealthCheck] START")

        val checks: List<Pair<String, () -> Boolean>> = listOf(
            "Ping 8.8.8.8" to {
                pingAddress("8.8.8.8", 53, "Google")
            },
            "DNS google.com" to {
                resolveDnsWithTimeout("google.com") != null
            },
            "Ping google.com (DNS)" to {
                pingAddress("google.com", 80, "GoogleDNS")
            },
            "Ping one.one.one.one (DNS)" to {
                pingAddress("one.one.one.one", 80, "OnesDNS")
            },
            "HTTP https://google.com/gen_204" to {
                httpPing("https://google.com/gen_204")
            }
        )

        var ok = true

        for ((name, check) in checks) {
            if (!runWithRetry(name = name, attempts = 2, block = check)) {
                ok = false
            }
        }

        if (!runWithRetry("VPN Interface Check", attempts = 1) {
                isVpnInterfaceExists()
            }) {
            ok = false
        }

        if (!runWithRetry("Tunnel heartbeat check", attempts = 1) {
                val mem = getTunnelMemoryUsage()
                currentMemoryUsageMb = mem
                mem >= 0
            }) {
            ok = false
        }

        if (currentMemoryUsageMb >= 0) {
            logger.log(
                "[HealthCheck] Memory usage: %.2f MB".format(currentMemoryUsageMb)
            )
        } else {
            logger.log("[HealthCheck] Memory usage: unknown")
        }

        logger.log("[HealthCheck] RESULT = $ok")
        return ok
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
        } catch (e: Throwable) {
            false
        }
    }

    // ---------- TCP Ping ----------

    private fun pingAddress(
        host: String,
        port: Int,
        name: String
    ): Boolean {
        val start = SystemClock.elapsedRealtime()
        return try {
            Socket().use { socket ->
                socket.connect(
                    InetSocketAddress(host, port),
                    timeoutMs.toInt()
                )
            }
            val ms = SystemClock.elapsedRealtime() - start
            logger.log("[ping $name] $ms ms")
            true
        } catch (e: Throwable) {
            logger.log("[ping $name] error: ${e.message}")
            false
        }
    }

    private fun isVpnInterfaceExists(): Boolean {
        return try {
            NetworkInterface.getNetworkInterfaces().toList().any { iface ->
                val name = iface.name.lowercase()
                name.contains("tun")
                        || name.contains("tap")
                        || name.contains("ppp")
                        || name.contains("ipsec")
                        || name.contains("vpn")
            }
        } catch (e: Throwable) {
            false
        }
    }

    private fun getTunnelMemoryUsage(): Double {
        return DobbyVpnService.instance?.getMemoryUsageMB() ?: -1.0
    }
}
