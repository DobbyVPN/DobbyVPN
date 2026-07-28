package com.dobby.feature.main.domain

import android.content.Context
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.PlatformServiceRegistry
import com.dobby.gomobile.dobbyvpn.Dobbyvpn
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.util.UUID

/** Android gomobile transport for the versioned Go session API. */
internal class AndroidSessionController(
    private val context: Context,
) : SessionController {
    private val mutex = Mutex()
    private var sessionId: String? = null

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> =
        withSession { id -> parse(Dobbyvpn.configureSession(id, commandId(), rawConfig)) { result ->
            SessionConfiguration(
                digest = result.string("digest"),
                profiles = result.array("profiles").map { profile ->
                    SessionProfile(profile.int("index"), profile.string("protocol").toSessionProtocol(), profile.string("description"))
                },
                warnings = result.array("warnings").map { warning -> SessionWarning(warning.string("code"), warning.string("message")) },
            )
        } }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> = withSession { id ->
        DobbyVpnService.requestShell(context, id)
        if (!PlatformServiceRegistry.awaitReady(5_000)) {
            return@withSession SessionControllerResult.Failure(
                message = "ANDROID_PLATFORM_UNAVAILABLE",
                code = SessionFailureCode.PLATFORM_FAILED,
            )
        }
        val mode = if (target is SessionStartTarget.AutoSelect) "AUTO_SELECT" else "PROFILE_INDEX"
        val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
        parse(Dobbyvpn.startSession(id, commandId(), mode, index)) { it.long("generation").toULong() }
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> = withSession { id ->
        parse(Dobbyvpn.stopSession(id, commandId(), generation.toLong())) { it.long("generation").toULong() }
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> = withSession { id ->
        parse(Dobbyvpn.snapshotSession(id)) { result ->
            SessionSnapshot(
                generation = result.long("generation").toULong(),
                state = result.string("state").toSessionState(),
                configured = result.bool("configured"),
                cleanupComplete = result.bool("cleanup_complete"),
                lastFailureCode = result.optionalString("last_failure")?.toSessionFailureCode(),
            )
        }
    }

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> = withSession { id ->
        parse(Dobbyvpn.observeSession(id, afterSequence.toLong())) { result ->
            SessionObservation(
                events = result.array("events").map { event ->
                    SessionEvent(
                        generation = event.long("generation").toULong(),
                        sequence = event.long("sequence").toULong(),
                        state = event.string("state").toSessionState(),
                        failureCode = event.optionalString("failure")?.toSessionFailureCode(),
                    )
                },
                nextSequence = result.long("next_sequence").toULong(),
            )
        }
    }

    override suspend fun destroy(): SessionControllerResult<Unit> = mutex.withLock {
        val id = sessionId ?: return@withLock SessionControllerResult.Success(Unit)
        when (val result = parse(Dobbyvpn.destroySession(id)) { Unit }) {
            is SessionControllerResult.Success -> { sessionId = null; result }
            is SessionControllerResult.Failure -> result
        }
    }

    private suspend fun <T> withSession(operation: suspend (String) -> SessionControllerResult<T>): SessionControllerResult<T> = mutex.withLock {
        val id = sessionId ?: when (val created = parse(Dobbyvpn.createSession()) { it.string("session_id") }) {
            is SessionControllerResult.Success -> created.value.also { sessionId = it }
            is SessionControllerResult.Failure -> return@withLock created
        }
        operation(id)
    }

    private fun commandId(): String = UUID.randomUUID().toString()

    private fun <T> parse(payload: String, transform: (JsonObject) -> T): SessionControllerResult<T> = runCatching {
        val root = json.parseToJsonElement(payload).jsonObject
        if (!root.bool("ok")) {
            val code = root["error"]?.jsonObject?.string("code") ?: "INTERNAL"
            return SessionControllerResult.Failure(message = code, code = code.toSessionFailureCode())
        }
        SessionControllerResult.Success(transform(root["result"]?.jsonObject ?: JsonObject(emptyMap())))
    }.getOrElse {
        SessionControllerResult.Failure(
            message = "INTERNAL",
            code = SessionFailureCode.INTERNAL,
        )
    }

    private companion object { val json = Json { ignoreUnknownKeys = true } }
}

private fun JsonObject.string(name: String): String = this[name]?.jsonPrimitive?.content.orEmpty()
private fun JsonObject.optionalString(name: String): String? =
    this[name]?.jsonPrimitive?.content?.takeIf(String::isNotBlank)
private fun JsonObject.long(name: String): Long = this[name]?.jsonPrimitive?.longOrNull ?: 0L
private fun JsonObject.int(name: String): Int = this[name]?.jsonPrimitive?.intOrNull ?: -1
private fun JsonObject.bool(name: String): Boolean = this[name]?.jsonPrimitive?.booleanOrNull ?: false
private fun JsonObject.array(name: String): List<JsonObject> =
    (this[name] as? JsonArray)?.mapNotNull { runCatching { it.jsonObject }.getOrNull() }.orEmpty()

private fun String.toSessionProtocol(): SessionProtocol = when (this) {
    "OUTLINE" -> SessionProtocol.OUTLINE
    "XRAY" -> SessionProtocol.XRAY
    "TRUST_TUNNEL" -> SessionProtocol.TRUST_TUNNEL
    "" -> SessionProtocol.UNSPECIFIED
    else -> SessionProtocol.UNKNOWN
}

private fun String.toSessionState(): SessionState = when (this) {
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
