package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepositoryXray
import com.dobby.feature.main.domain.XrayClientConfig
import com.dobby.feature.main.domain.clearXrayConfig
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import net.peanuuutz.tomlkt.TomlArray
import net.peanuuutz.tomlkt.TomlElement
import net.peanuuutz.tomlkt.TomlLiteral
import net.peanuuutz.tomlkt.TomlNull
import net.peanuuutz.tomlkt.TomlTable

internal class XrayTomlApplier(
    private val xrayRepo: DobbyConfigsRepositoryXray,
    private val logger: Logger,
) {
    private val json = Json {
        prettyPrint = true
        ignoreUnknownKeys = true
        encodeDefaults = true
    }

    fun apply(config: XrayClientConfig): Boolean {
        logger.log("Applying generic [Xray] configuration")

        // Xray usually needs at least one outbound
        if (config.outbounds == null) {
            logger.log("Invalid [Xray]: Config is empty (no outbounds).")
            xrayRepo.clearXrayConfig()
            return false
        }

        val xrayJsonString = buildXrayJson(config)

        xrayRepo.setXrayConfig(xrayJsonString)
        xrayRepo.setIsXrayEnabled(true)

        logger.log("Xray config applied successfully.")
        return true
    }

    private fun buildXrayJson(config: XrayClientConfig): String {
        val rootMap = mutableMapOf<String, JsonElement>()

        fun add(key: String, value: TomlElement?) {
            if (value != null) rootMap[key] = convertTomlToJson(value)
        }

        add("version", config.version)
        add("log", config.log)
        add("api", config.api)
        add("dns", config.dns)
        add("routing", config.routing)
        add("policy", config.policy)
        add("inbounds", config.inbounds)
        add("outbounds", config.outbounds)
        add("transport", config.transport)
        add("stats", config.stats)
        add("reverse", config.reverse)
        add("fakedns", config.fakedns)
        add("metrics", config.metrics)
        add("observatory", config.observatory)
        add("burstObservatory", config.burstObservatory)

        // logger.log("Got map: $rootMap")

        // logger.log("Got json: ${json.encodeToString(JsonObject(rootMap))}")

        return json.encodeToString(JsonObject(rootMap))
    }

    private fun convertTomlToJson(element: TomlElement): JsonElement {
        return when (element) {
            is TomlTable -> {
                val map = element.entries.associate { (key, value) ->
                    key to convertTomlToJson(value)
                }
                JsonObject(map)
            }
            is TomlArray -> {
                val list = element.map { convertTomlToJson(it) }
                JsonArray(list)
            }
            is TomlLiteral -> {
                // Convert literal string value to appropriate JsonPrimitive
                // TOML parser usually gives us a string representation.
                // We try to parse it back to specific types for JSON.
                val content = element.content

                // Try Boolean
                if (content.equals("true", ignoreCase = true)) return JsonPrimitive(true)
                if (content.equals("false", ignoreCase = true)) return JsonPrimitive(false)

                // Try Integer/Long
                val longVal = content.toLongOrNull()
                if (longVal != null) return JsonPrimitive(longVal)

                // Try Double
                val doubleVal = content.toDoubleOrNull()
                if (doubleVal != null) return JsonPrimitive(doubleVal)

                // Fallback to String
                JsonPrimitive(content)
            }
            is TomlNull -> JsonNull
        }
    }
}