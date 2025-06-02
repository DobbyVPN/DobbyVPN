package com.dobby.feature.diagnostic

import com.dobby.feature.diagnostic.domain.IpData
import com.dobby.feature.diagnostic.domain.IpRepository
import com.dobby.feature.logging.Logger
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.net.InetAddress
import java.util.concurrent.TimeUnit

class IpRepositoryImpl(
    private val logger: Logger
) : IpRepository {

    override fun getIpData(): IpData {
        val client = OkHttpClient.Builder()
            .connectTimeout(5, TimeUnit.SECONDS)
            .writeTimeout(5, TimeUnit.SECONDS)
            .readTimeout(5, TimeUnit.SECONDS)
            .build()
        val url = "https://ipinfo.io/json"
        val request: Request = Request.Builder()
            .url(url)
            .build()

        val response = try {
            client.newCall(request).execute()
        } catch (e: Exception) {
            logger.log("[Diagnostic] Sending request to $url, failed: ${e.message}")

            return IpData(
                ip = "Failed",
                city = "",
                country = "",
            )
        }

        logger.log("[Diagnostic] Sending request to $url, status code: ${response.code}")

        if (response.isSuccessful) {
            val content = response.body?.string() ?: return IpData("", "", "")
            val json = Json { ignoreUnknownKeys = true }
            val responseJson = json.decodeFromString<IpInfoRequestJsonData>(content)

            return IpData(
                ip = responseJson.ip,
                city = responseJson.city,
                country = responseJson.country,
            )
        } else {
            return IpData(
                ip = "null",
                city = "null",
                country = "null",
            )
        }
    }

    override fun getHostnameIpData(hostname: String): IpData {
        val client = OkHttpClient.Builder()
            .connectTimeout(5, TimeUnit.SECONDS)
            .writeTimeout(5, TimeUnit.SECONDS)
            .readTimeout(5, TimeUnit.SECONDS)
            .build()

        val address: InetAddress

        try {
            address = InetAddress.getByName(hostname)
            logger.log("IP address for $hostname: ${address.hostAddress}")
        } catch (e: Exception) {
            logger.log("[Diagnostic] Error resolving $hostname: ${e.message}")

            return IpData(
                ip = "Failed",
                city = "",
                country = "",
            )
        }

        val url = "https://ipinfo.io/${address.hostAddress}/json"

        val request: Request = Request.Builder()
            .url(url)
            .build()

        val response = try {
            client.newCall(request).execute()
        } catch (e: Exception) {
            logger.log("[Diagnostic] Sending request to $url, failed: ${e.message}")

            return IpData(
                ip = "Failed",
                city = "",
                country = "",
            )
        }

        logger.log("[Diagnostic] Sending request to $url, status code: ${response.code}")

        if (response.isSuccessful) {
            val content = response.body?.string() ?: return IpData("", "", "")
            val json = Json { ignoreUnknownKeys = true }
            val responseJson = json.decodeFromString<IpInfoRequestJsonData>(content)

            return IpData(
                ip = responseJson.ip,
                city = responseJson.city,
                country = responseJson.country,
            )
        } else {
            return IpData(
                ip = "null",
                city = "null",
                country = "null",
            )
        }
    }

    @Serializable
    private data class IpInfoRequestJsonData(
        val ip: String,
        val city: String,
        val country: String
    )
}
