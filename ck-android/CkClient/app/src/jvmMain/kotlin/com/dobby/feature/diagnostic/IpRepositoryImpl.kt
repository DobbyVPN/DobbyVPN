package com.dobby.feature.diagnostic

import com.dobby.feature.diagnostic.domain.IpData
import com.dobby.feature.diagnostic.domain.IpRepository
import com.dobby.feature.logging.Logger
import kotlinx.coroutines.future.await
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse

class IpRepositoryImpl(
    private val logger: Logger
) : IpRepository {
    override suspend fun getIpData(): IpData {
        val client = HttpClient.newBuilder().build();
        val uri = URI.create("https://ipinfo.io/json")
        val request = HttpRequest.newBuilder()
            .uri(uri)
            .build()

        val response = client.sendAsync(request, HttpResponse.BodyHandlers.ofString()).await()
        logger.log("[Diagnostic] Sending request to $uri, status code: ${response.statusCode()}")

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
