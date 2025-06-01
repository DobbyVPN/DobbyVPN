package com.dobby.feature.diagnostic

import com.dobby.feature.diagnostic.domain.IpData
import com.dobby.feature.diagnostic.domain.IpRepository
import com.dobby.feature.logging.Logger
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request

class IpRepositoryImpl(
    private val logger: Logger
) : IpRepository {
    private val client = OkHttpClient()

    override fun getIpData(): IpData {
        val url = "https://ipinfo.io/json"
        val request: Request = Request.Builder()
            .url(url)
            .build()

        client.newCall(request).execute().use { response ->
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
    }

    override fun getHostnameIpData(hostname: String): IpData {
        return IpData("Not implemented", "", "")
    }

    @Serializable
    private data class IpInfoRequestJsonData(
        val ip: String,
        val city: String,
        val country: String
    )
}
