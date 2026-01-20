package com.dobby.feature.vpn_service.domain

import android.content.Context
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionStateRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.BufferedReader
import java.io.InputStreamReader
import java.util.concurrent.TimeUnit

class GeoRoutingService(
    private val logger: Logger,
    private val connectionStateRepository: ConnectionStateRepository,
    private val context: Context
) {
    suspend fun getCountryCode(): String? {
        return withContext(Dispatchers.IO) {
            // VPN connection check.
            val isVpnConnected = connectionStateRepository.flow.value
            if (isVpnConnected) {
                logger.log("GeoRouting: VPN is connected, cannot fetch country")
                return@withContext null
            }

            try {
                val result = withTimeoutOrNull(10000L) {
                    val client = OkHttpClient.Builder()
                        .connectTimeout(5, TimeUnit.SECONDS)
                        .readTimeout(5, TimeUnit.SECONDS)
                        .build()

                    val url = "https://ipinfo.io/json"
                    val request = Request.Builder()
                        .url(url)
                        .build()

                    val response = client.newCall(request).execute()

                    if (response.isSuccessful) {
                        val content = response.body?.string() ?: return@withTimeoutOrNull null
                        val json = Json { ignoreUnknownKeys = true }
                        val ipInfo = json.decodeFromString<IpInfoResponse>(content)
                        logger.log("GeoRouting: Country code fetched: ${ipInfo.country}")
                        ipInfo.country
                    } else {
                        logger.log("GeoRouting: Failed to fetch country, status: ${response.code}")
                        null
                    }
                }

                result
            } catch (e: Exception) {
                logger.log("GeoRouting: Error fetching country: ${e.message}")
                null
            }
        }
    }

    suspend fun getCountryIpRanges(countryCode: String): List<String> {
        return withContext(Dispatchers.IO) {
            try {
                val result = withTimeoutOrNull(60000L) {
                    val client = OkHttpClient.Builder()
                        .connectTimeout(10, TimeUnit.SECONDS)
                        .readTimeout(10, TimeUnit.SECONDS)
                        .build()

                    val cidrRanges = fetchCountryCidrRanges(countryCode, client)

                    cidrRanges
                }

                result ?: emptyList()
            } catch (e: Exception) {
                logger.log("GeoRouting: Error fetching IP ranges: ${e.message}")
                emptyList()
            }
        }
    }

    private suspend fun fetchCountryCidrRanges(
        countryCode: String,
        client: OkHttpClient
    ): List<String> {
        val cidrRanges = mutableListOf<String>()

        try {
            logger.log("GeoRouting: Attempting to fetch CIDR ranges for $countryCode")

            // Try to load from assets (generated during CI build)
            val assetsCidrRanges = loadCidrRangesFromAssets(countryCode.uppercase())
            if (assetsCidrRanges.isNotEmpty()) {
                cidrRanges.addAll(assetsCidrRanges)
            } else {
                logger.log("GeoRouting: No CIDR file found in assets for $countryCode")
            }
        } catch (e: Exception) {
            logger.log("GeoRouting: Error in fetchCountryCidrRanges: ${e.message}")
        }

        return cidrRanges
    }

    private suspend fun loadCidrRangesFromAssets(countryCode: String): List<String> {
        val cidrRanges = mutableListOf<String>()
        
        try {
            val fileName = "cidr/$countryCode.cidr"
            logger.log("GeoRouting: Starting to load CIDR file from assets: $fileName")
            
            val inputStream = context.assets.open(fileName)
            
            var lineCount = 0
            BufferedReader(InputStreamReader(inputStream), 8192).use { reader ->
                var line: String?
                while (reader.readLine().also { line = it } != null) {
                    lineCount++
                    val trimmed = line?.trim() ?: continue
                    
                    if (trimmed.isNotEmpty() && !trimmed.startsWith("#")) {
                        if (trimmed.matches(Regex("^\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}/\\d{1,2}$"))) {
                            cidrRanges.add(trimmed)
                        }
                    }
                    
                    // Log progress for large files
                    if (lineCount % 10000 == 0) {
                        logger.log("GeoRouting: Loaded $lineCount lines, ${cidrRanges.size} valid CIDR ranges so far...")
                    }
                }
            }
            
            logger.log("GeoRouting: Successfully loaded ${cidrRanges.size} CIDR ranges from assets/$fileName (total lines: $lineCount)")
        } catch (e: Exception) {
            logger.log("GeoRouting: Error loading CIDR from assets: ${e.message}")
            e.printStackTrace()
        }
        
        return cidrRanges
    }

    @Serializable
    private data class IpInfoResponse(
        val ip: String,
        val hostname: String? = null,
        val city: String? = null,
        val region: String? = null,
        val country: String,
        val loc: String? = null,
        val org: String? = null,
        val postal: String? = null,
        val timezone: String? = null,
        val readme: String? = null
    )
}
