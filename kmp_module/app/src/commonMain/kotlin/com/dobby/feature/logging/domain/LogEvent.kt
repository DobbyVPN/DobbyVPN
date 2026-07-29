package com.dobby.feature.logging.domain

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

private const val LOG_SCHEMA = "dobby.log/v1"
private val stableEventName = Regex("[a-z0-9][a-z0-9_.-]*")
private val logJson = Json { ignoreUnknownKeys = true }

enum class LogLevel {
    TRACE,
    DEBUG,
    INFO,
    WARN,
    ERROR;

    companion object {
        private val legacyError = Regex(
            "(?i)(^|\\W)(failed|failure|error|exception|fatal|panic|cannot|can't)(\\W|$)",
        )
        private val legacyWarning = Regex(
            "(?i)(^|\\W)(warning|warn|retry|fallback|degraded)(\\W|$)",
        )

        fun fromLegacyMessage(message: String): LogLevel = when {
            message.contains("[TRACE]", ignoreCase = true) -> TRACE
            message.contains("[DEBUG]", ignoreCase = true) -> DEBUG
            message.contains("[ERROR]", ignoreCase = true) -> ERROR
            message.contains("[WARN]", ignoreCase = true) ||
                message.contains("[WARNING]", ignoreCase = true) ||
                message.contains(" WARNING:", ignoreCase = true) -> WARN
            legacyError.containsMatchIn(message) -> ERROR
            legacyWarning.containsMatchIn(message) -> WARN
            else -> INFO
        }
    }
}

internal fun encodeLogEvent(
    timestamp: String,
    level: LogLevel,
    source: String,
    event: String,
    message: String,
    fields: Map<String, String> = emptyMap(),
): String = buildJsonObject {
    put("schema", LOG_SCHEMA)
    put("timestamp", timestamp)
    put("level", level.name)
    put("source", source.takeIf(stableEventName::matches) ?: "app")
    put("event", event.takeIf(stableEventName::matches) ?: "log.message")
    put("message", redactLog(message))
    if (fields.isNotEmpty()) {
        put(
            "fields",
            buildJsonObject {
                fields.toSortedMap().forEach { (key, value) ->
                    put(key, redactLogField(key, value))
                }
            },
        )
    }
}.toString()

internal fun renderLogLine(line: String): String {
    if (!line.startsWith("{")) return line
    return runCatching {
        val event = logJson.parseToJsonElement(line).jsonObject
        if (event["schema"]?.jsonPrimitive?.content != LOG_SCHEMA) return@runCatching line
        val timestamp = event["timestamp"]?.jsonPrimitive?.content.orEmpty()
        val level = event["level"]?.jsonPrimitive?.content.orEmpty().ifEmpty { LogLevel.INFO.name }
        val source = event["source"]?.jsonPrimitive?.content.orEmpty()
        val message = event["message"]?.jsonPrimitive?.content.orEmpty()
        val readableTimestamp = timestamp
            .replace('T', ' ')
            .removeSuffix("Z")
            .take(23)
        val attributes = buildList {
            (event["fields"] as? JsonObject)?.toSortedMap()?.forEach { (key, value) ->
                add("$key=${value.jsonPrimitive.content}")
            }
            event.toSortedMap().forEach { (key, value) ->
                if (key !in setOf("schema", "timestamp", "level", "source", "event", "category", "message", "fields")) {
                    val readableValue = (value as? JsonPrimitive)?.content ?: value.toString()
                    add("$key=$readableValue")
                }
            }
        }
        val details = attributes.takeIf { it.isNotEmpty() }?.joinToString(prefix = " · ", separator = " · ").orEmpty()
        val readableSource = source.takeIf { it.isNotEmpty() }?.let { " [$it]" }.orEmpty()
        "[$readableTimestamp] [${level.padEnd(5)}]$readableSource $message$details"
    }.getOrDefault(line)
}

internal fun logEventName(line: String): String? {
    if (!line.startsWith("{")) return null
    return runCatching {
        logJson.parseToJsonElement(line).jsonObject["event"]?.jsonPrimitive?.content
    }.getOrNull()
}

internal fun comparableLogTimestamp(line: String): String? {
    if (line.startsWith("{")) {
        val timestamp = runCatching {
            logJson.parseToJsonElement(line).jsonObject["timestamp"]?.jsonPrimitive?.content
        }.getOrNull() ?: return null
        if (timestamp.length < 19) return null
        return timestamp.replace('T', ' ')
    }
    if (line.length < 21 || line.first() != '[' || line[20] != ']') return null
    val timestamp = line.substring(1, 20)
    val looksValid = timestamp.length == 19 &&
        timestamp[4] == '-' && timestamp[7] == '-' && timestamp[10] == ' ' &&
        timestamp[13] == ':' && timestamp[16] == ':'
    return timestamp.takeIf { looksValid }
}
