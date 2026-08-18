package com.dobby.feature.main.domain

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/** Decodes the stable, safe JSON envelope returned by every session transport. */
internal object SessionEnvelopeDecoder {
    private val json = Json { ignoreUnknownKeys = true }

    fun <T> decode(payload: String, transform: (JsonObject) -> T): SessionControllerResult<T> = runCatching {
        val root = json.parseToJsonElement(payload).jsonObject
        if (!root.sessionBool("ok")) {
            val code = root["error"]?.jsonObject?.sessionString("code") ?: "INTERNAL"
            return SessionControllerResult.Failure(message = code, code = code.toSessionFailureCode())
        }
        SessionControllerResult.Success(transform(root["result"]?.jsonObject ?: JsonObject(emptyMap())))
    }.getOrElse {
        SessionControllerResult.Failure(message = "INTERNAL", code = SessionFailureCode.INTERNAL)
    }
}

internal fun JsonObject.sessionString(name: String): String = this[name]?.jsonPrimitive?.content.orEmpty()

internal fun JsonObject.sessionOptionalString(name: String): String? =
    this[name]?.jsonPrimitive?.content?.takeIf(String::isNotBlank)

internal fun JsonObject.sessionLong(name: String): Long = this[name]?.jsonPrimitive?.longOrNull ?: 0L

internal fun JsonObject.sessionInt(name: String): Int = this[name]?.jsonPrimitive?.intOrNull ?: -1

internal fun JsonObject.sessionBool(name: String): Boolean = this[name]?.jsonPrimitive?.booleanOrNull ?: false

internal fun JsonObject.sessionArray(name: String): List<JsonObject> =
    (this[name] as? JsonArray)?.mapNotNull { runCatching { it.jsonObject }.getOrNull() }.orEmpty()

internal fun String.toSessionProtocol(): SessionProtocol = when (this) {
    "OUTLINE" -> SessionProtocol.OUTLINE
    "XRAY" -> SessionProtocol.XRAY
    "TRUST_TUNNEL" -> SessionProtocol.TRUST_TUNNEL
    "" -> SessionProtocol.UNSPECIFIED
    else -> SessionProtocol.UNKNOWN
}

internal fun String.toSessionState(): SessionState = when (this) {
    "IDLE" -> SessionState.IDLE
    "CONFIGURED" -> SessionState.CONFIGURED
    "PROBING" -> SessionState.PROBING
    "PREPARING" -> SessionState.PREPARING
    "CONNECTED" -> SessionState.CONNECTED
    "STOPPING" -> SessionState.STOPPING
    "FAILED" -> SessionState.FAILED
    "DESTROYED" -> SessionState.DESTROYED
    "" -> SessionState.UNSPECIFIED
    else -> SessionState.UNKNOWN
}

internal fun JsonObject.toSessionConfiguration(): SessionConfiguration = SessionConfiguration(
    digest = sessionString("digest"),
    profiles = sessionArray("profiles").map { profile ->
        SessionProfile(
            index = profile.sessionInt("index"),
            protocol = profile.sessionString("protocol").toSessionProtocol(),
            description = profile.sessionString("description"),
        )
    },
    warnings = sessionArray("warnings").map { warning ->
        SessionWarning(warning.sessionString("code"), warning.sessionString("message"))
    },
)

internal fun JsonObject.toSessionSnapshot(): SessionSnapshot = SessionSnapshot(
    generation = sessionLong("generation").toULong(),
    state = sessionString("state").toSessionState(),
    configured = sessionBool("configured"),
    cleanupComplete = sessionBool("cleanup_complete"),
    lastFailureCode = sessionOptionalString("last_failure")?.toSessionFailureCode(),
)

internal fun JsonObject.toSessionObservation(): SessionObservation = SessionObservation(
    events = sessionArray("events").map { event ->
        SessionEvent(
            generation = event.sessionLong("generation").toULong(),
            sequence = event.sessionLong("sequence").toULong(),
            state = event.sessionString("state").toSessionState(),
            failureCode = event.sessionOptionalString("failure")?.toSessionFailureCode(),
            sessionId = event.sessionString("session_id"),
        )
    },
    nextSequence = sessionLong("next_sequence").toULong(),
)
