package com.dobby.feature.vpn_service.domain

import com.dobby.feature.logging.Logger
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.net.HttpURLConnection
import java.net.URL

class IpFetcher(
    private val logger: Logger
) {

    suspend fun fetchIp(): String? {
        return withContext(Dispatchers.IO) {
            try {
                val result = withTimeoutOrNull(7000L) {
                    val url = URL("https://api.ipify.org")
                    val connection = url.openConnection() as HttpURLConnection

                    connection.connectTimeout = 5000
                    connection.readTimeout = 5000

                    connection.inputStream.bufferedReader().use { reader ->
                        reader.readText()
                    }.also { ipAddress ->
                        if (ipAddress.isNotEmpty()) {
                            return@withTimeoutOrNull ipAddress
                        } else {
                            return@withTimeoutOrNull null
                        }
                    }
                }

                if (result == null) {
                    logger.log("VpnService: Timeout or empty response while fetching IP")
                }
                result
            } catch (e: Exception) {
                logger.log("Error fetching IP: ${e.message}")
                null
            }
        }
    }
}