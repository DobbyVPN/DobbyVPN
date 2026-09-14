package com.dobby.feature.main.domain

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/** Decodes the small JSON boundary used by gomobile. */
internal object SessionEnvelopeDecoder {
    private val json = Json

    fun <T> decode(payload: String, transform: (JsonObject) -> T): SessionControllerResult<T> {
        val root = json.parseToJsonElement(payload).jsonObject
        val ok = root["ok"]?.jsonPrimitive?.booleanOrNull ?: error("missing or invalid ok")
        if (!ok) {
            val failure = root["error"]?.jsonObject ?: error("failure envelope has no error")
            return SessionControllerResult.Failure(
                message = failure.sessionString("message").also { require(it.isNotBlank()) },
                code = failure.sessionString("code").toSessionFailureCode(),
            )
        }
        return SessionControllerResult.Success(transform(root["result"]?.jsonObject ?: JsonObject(emptyMap())))
    }
}

internal fun JsonObject.sessionString(name: String): String =
    this[name]?.jsonPrimitive?.content ?: error("missing or invalid $name")

internal fun JsonObject.sessionOptionalString(name: String): String? =
    this[name]?.jsonPrimitive?.content?.takeIf(String::isNotBlank)

internal fun JsonObject.requiredSessionLong(name: String): Long =
    this[name]?.jsonPrimitive?.longOrNull ?: error("missing or invalid $name")

internal fun JsonObject.requiredNonNegativeSessionLong(name: String): Long =
    requiredSessionLong(name).also { require(it >= 0) }

internal fun JsonObject.requiredPositiveSessionLong(name: String): Long =
    requiredSessionLong(name).also { require(it > 0) }

internal fun JsonObject.sessionInt(name: String): Int =
    this[name]?.jsonPrimitive?.intOrNull ?: error("missing or invalid $name")

internal fun JsonObject.sessionBool(name: String): Boolean {
	val value = this[name]?.jsonPrimitive ?: error("missing or invalid $name")
	if (value.isString) error("missing or invalid $name")
	return value.booleanOrNull ?: error("missing or invalid $name")
}

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
    else -> error("unsupported session state: $this")
}

private fun String.toSessionSourceKind(): SessionSourceKind = when (this) {
    "INLINE" -> SessionSourceKind.INLINE
    "URL" -> SessionSourceKind.URL
    "" -> SessionSourceKind.INLINE
    else -> error("unsupported session source kind: $this")
}

internal fun JsonObject.toSessionConfiguration(sessionId: String): SessionConfiguration = SessionConfiguration(
    sessionId = sessionId,
    sequence = requiredNonNegativeSessionLong("sequence").toULong(),
    digest = sessionString("digest"),
    sourceKind = sessionString("source_kind").toSessionSourceKind(),
    profiles = sessionArray("profiles").map { profile ->
        SessionProfile(
            profile.sessionInt("index"),
            profile.sessionString("protocol").toSessionProtocol(),
            profile.sessionString("description"),
        )
    },
    warnings = sessionArray("warnings").map { warning ->
        SessionWarning(warning.sessionString("code"), warning.sessionString("message"))
    },
)

internal fun JsonObject.toSessionSnapshot(): SessionSnapshot {
    val failure = this["last_failure"]?.let { value ->
        val obj = value.jsonObject
        SessionFailure(
            code = obj.sessionString("code").toSessionFailureCode(),
            message = obj.sessionString("message"),
        )
    }
    val activeProfile = this["active_profile"]?.jsonObject?.toSessionProfile()
    return SessionSnapshot(
        sessionId = sessionString("session_id"),
        sequence = requiredNonNegativeSessionLong("sequence").toULong(),
        generation = requiredNonNegativeSessionLong("generation").toULong(),
        state = sessionString("state").toSessionState(),
        configured = sessionBool("configured"),
        digest = sessionString("digest"),
        sourceKind = sessionString("source_kind").toSessionSourceKind(),
        profiles = sessionArray("profiles").map(JsonObject::toSessionProfile),
        warnings = sessionArray("warnings").map { SessionWarning(it.sessionString("code"), it.sessionString("message")) },
        activeProfile = activeProfile,
        lastFailure = failure,
        cleanupComplete = sessionBool("cleanup_complete"),
        recovering = sessionBool("recovering"),
    )
}

private fun JsonObject.toSessionProfile() = SessionProfile(
    index = sessionInt("index"),
    protocol = sessionString("protocol").toSessionProtocol(),
    description = sessionString("description"),
)
