package com.dobby.feature.main.domain

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/** Decodes the stable JSON envelope returned by every session transport. */
internal object SessionEnvelopeDecoder {
    private val json = Json

    fun <T> decode(payload: String, transform: (JsonObject) -> T): SessionControllerResult<T> {
        val root = json.parseToJsonElement(payload).jsonObject
        val ok = root["ok"]?.jsonPrimitive?.booleanOrNull ?: error("missing or invalid ok")
        if (!ok) {
            val failure = root["error"]?.jsonObject ?: error("failure envelope has no error")
            val code = failure.sessionString("code").also { require(it.isNotBlank()) }
            return SessionControllerResult.Failure(
                message = failure.sessionString("message").also { require(it.isNotBlank()) },
                code = code.toSessionFailureCode(),
            )
        }
        return SessionControllerResult.Success(transform(root["result"]?.jsonObject ?: JsonObject(emptyMap())))
    }
}

internal fun JsonObject.sessionString(name: String): String =
    this[name]?.jsonPrimitive?.content ?: error("missing or invalid $name")

internal fun JsonObject.sessionOptionalString(name: String): String? =
    this[name]?.jsonPrimitive?.content?.takeIf(String::isNotBlank)

internal fun JsonObject.requiredSessionIdentifier(name: String): String =
    sessionString(name).also { value ->
        require(value.isNotEmpty() && value.all { it.isLetterOrDigit() || it == '-' || it == '.' || it == '_' })
    }

/** Required for Go-owned monotonic values; never manufacture a zero cursor. */
internal fun JsonObject.requiredSessionLong(name: String): Long =
    this[name]?.jsonPrimitive?.longOrNull ?: error("missing or invalid $name")

internal fun JsonObject.requiredSessionSequence(): Long =
    requiredSessionLong("sequence").also { require(it > 0) }

/** Allows the initial zero cursor but never accepts a negative Go value. */
internal fun JsonObject.requiredNonNegativeSessionLong(name: String): Long =
    requiredSessionLong(name).also { require(it >= 0) }

internal fun JsonObject.requiredPositiveSessionLong(name: String): Long =
    requiredSessionLong(name).also { require(it > 0) }

internal fun JsonObject.sessionInt(name: String): Int =
    this[name]?.jsonPrimitive?.intOrNull ?: error("missing or invalid $name")

internal fun JsonObject.sessionBool(name: String): Boolean =
    this[name]?.jsonPrimitive?.booleanOrNull ?: error("missing or invalid $name")

internal fun JsonObject.sessionArray(name: String): List<JsonObject> =
    (this[name] as? JsonArray)?.map { it.jsonObject } ?: error("missing or invalid $name")

internal fun String.toSessionProtocol(): SessionProtocol = when (this) {
    "OUTLINE" -> SessionProtocol.OUTLINE
    "XRAY" -> SessionProtocol.XRAY
    "TRUST_TUNNEL" -> SessionProtocol.TRUST_TUNNEL
    else -> error("unsupported session protocol: $this")
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
    else -> error("unsupported session state: $this")
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

internal fun JsonObject.toSessionSnapshot(): SessionSnapshot {
    val state = sessionString("state").toSessionState()
    return SessionSnapshot(
        generation = requiredNonNegativeSessionLong("generation").toULong(),
        state = state,
        configured = sessionBool("configured"),
        cleanupComplete = sessionBool("cleanup_complete"),
        lastFailureCode = sessionOptionalString("last_failure")?.toSessionFailureCode(),
        sessionId = requiredSessionIdentifier("session_id"),
    )
}

internal fun JsonObject.toSessionObservation(): SessionObservation {
    val events = sessionArray("events").map { event ->
        val state = event.sessionString("state").toSessionState()
        val generation = event.requiredNonNegativeSessionLong("generation")
        if (state in setOf(
                SessionState.PROBING,
                SessionState.PREPARING,
                SessionState.CONNECTED,
                SessionState.STOPPING,
            )) {
            require(generation > 0)
        }
        SessionEvent(
            sessionId = event.requiredSessionIdentifier("session_id"),
            generation = generation.toULong(),
            sequence = event.requiredSessionSequence().toULong(),
            state = state,
            failureCode = event.sessionOptionalString("failure")?.toSessionFailureCode(),
        )
    }
    // One Observe response is scoped to one Go session. Reject a mixed
    // response instead of allowing a foreign sequence to advance the cursor
    // before the UI can notice the identity mismatch.
    require(events.map { it.sessionId }.distinct().size <= 1)
    return SessionObservation(
        events = events,
        nextSequence = requiredNonNegativeSessionLong("next_sequence").toULong(),
    )
}
