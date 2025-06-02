package com.dobby.feature.diagnostic

import com.dobby.feature.diagnostic.domain.IpData
import com.dobby.feature.diagnostic.domain.IpRepository
import com.dobby.feature.logging.Logger
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.net.InetAddress
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration

class IpRepositoryImpl(
    private val logger: Logger
) : IpRepository {
    override fun getIpData(): IpData {
        val client = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(2))
            .build()
        val url = "https://ipinfo.io/json"
        val uri = URI.create(url)
        val request = HttpRequest.newBuilder()
            .uri(uri)
            .timeout(Duration.ofSeconds(2))
            .build()

        val response = try {
            client.send(request, HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            logger.log("[Diagnostic] Sending request to $url, failed: ${e.message}")

            return IpData(
                ip = "Failed",
                city = "",
                country = "",
            )
        }

        logger.log("[Diagnostic] Sending request to $url, status code: ${response.statusCode()}")

        if (response.statusCode() == 200) {
            val json = Json { ignoreUnknownKeys = true }
            val responseJson = json.decodeFromString<IpInfoRequestJsonData>(response.body())

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
        val client = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(2))
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
        val uri = URI.create(url)
        val request = HttpRequest.newBuilder()
            .uri(uri)
            .timeout(Duration.ofSeconds(2))
            .build()

        val response = try {
            client.send(request, HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            logger.log("[Diagnostic] Sending request to $url, failed: ${e.message}")

            return IpData(
                ip = "Failed",
                city = "",
                country = "",
            )
        }

        logger.log("[Diagnostic] Sending request to $url, status code: ${response.statusCode()}")

        if (response.statusCode() == 200) {
            val json = Json { ignoreUnknownKeys = true }
            val responseJson = json.decodeFromString<IpInfoRequestJsonData>(response.body())

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
