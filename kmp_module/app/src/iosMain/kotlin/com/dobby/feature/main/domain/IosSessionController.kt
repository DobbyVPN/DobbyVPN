package com.dobby.feature.main.domain

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

/**
 * Swift owns the app/NetworkExtension process boundary.  In particular, it
 * persists the exact configuration bytes to the App Group and starts the
 * extension; the extension is the process that creates and owns the Go
 * session.  The bridge returns only sessionapi's safe JSON envelopes.
 */
interface IosSessionBridge {
    fun configure(rawConfig: ByteArray): String
    fun start(mode: String, index: Int): String
    fun stop(generation: Long): String
    fun snapshot(): String
    fun observe(afterSequence: Long): String
    fun destroy(): String
}

object IosSessionBridgeRegistry {
    private var installed: IosSessionBridge? = null

    fun install(bridge: IosSessionBridge) {
        installed = bridge
    }

    fun requireBridge(): IosSessionBridge = checkNotNull(installed) {
        "iOS session bridge must be installed before StartDI"
    }
}

/** A thin KMP adapter; no TOML/profile/failover decision exists on iOS. */
internal class IosSessionController(
    private val bridge: IosSessionBridge,
) : SessionController {
    private val mutex = Mutex()

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> = mutex.withLock {
        parse(bridge.configure(rawConfig)) { result ->
            SessionConfiguration(
                digest = result.string("digest"),
                profiles = result.array("profiles").map { profile ->
                    SessionProfile(profile.int("index"), profile.string("protocol").toIosSessionProtocol(), profile.string("description"))
                },
                warnings = result.array("warnings").map { warning -> SessionWarning(warning.string("code"), warning.string("message")) },
            )
        }
    }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> = mutex.withLock {
        val mode = if (target is SessionStartTarget.AutoSelect) "AUTO_SELECT" else "PROFILE_INDEX"
        val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
        parse(bridge.start(mode, index)) { it.long("generation").toULong() }
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> = mutex.withLock {
        parse(bridge.stop(generation.toLong())) { it.long("generation").toULong() }
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> = mutex.withLock {
        parse(bridge.snapshot()) { result ->
            SessionSnapshot(
                generation = result.long("generation").toULong(),
                state = result.string("state").toIosSessionState(),
                configured = result.bool("configured"),
                cleanupComplete = result.bool("cleanup_complete"),
                lastFailureCode = result.optionalString("last_failure")?.toSessionFailureCode(),
            )
        }
    }

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> = mutex.withLock {
        parse(bridge.observe(afterSequence.toLong())) { result ->
            SessionObservation(
                events = result.array("events").map { event ->
                    SessionEvent(
                        generation = event.long("generation").toULong(),
                        sequence = event.long("sequence").toULong(),
                        state = event.string("state").toIosSessionState(),
                        failureCode = event.optionalString("failure")?.toSessionFailureCode(),
                    )
                },
                nextSequence = result.long("next_sequence").toULong(),
            )
        }
    }

    override suspend fun destroy(): SessionControllerResult<Unit> = mutex.withLock {
        parse(bridge.destroy()) { Unit }
    }

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

private fun String.toIosSessionProtocol(): SessionProtocol = when (this) {
    "OUTLINE" -> SessionProtocol.OUTLINE
    "XRAY" -> SessionProtocol.XRAY
    "TRUST_TUNNEL" -> SessionProtocol.TRUST_TUNNEL
    "" -> SessionProtocol.UNSPECIFIED
    else -> SessionProtocol.UNKNOWN
}

private fun String.toIosSessionState(): SessionState = when (this) {
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
