package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow

/**
 * Platform boundary for the sessionapi/v2 lifecycle.
 *
 * Configuration is deliberately opaque: callers pass the bytes they acquired and this
 * controller owns the platform session and command identifiers used by the transport.
 */
interface SessionController {
    suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration>
    suspend fun start(target: SessionStartTarget = SessionStartTarget.AutoSelect): SessionControllerResult<ULong>
    suspend fun stop(generation: ULong): SessionControllerResult<ULong>
    suspend fun snapshot(): SessionControllerResult<SessionSnapshot>
    suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation>
    /** Ordered push events. Mobile shells may use native callbacks instead. */
    fun watch(afterSequence: ULong): Flow<SessionEvent> = emptyFlow()
    suspend fun destroy(): SessionControllerResult<Unit>
}

sealed interface SessionControllerResult<out T> {
    data class Success<T>(val value: T) : SessionControllerResult<T>
    data class Failure(
        val message: String,
        val code: SessionFailureCode = SessionFailureCode.UNKNOWN,
    ) : SessionControllerResult<Nothing>
}

data class SessionConfiguration(
    val digest: String,
    val profiles: List<SessionProfile>,
    val warnings: List<SessionWarning>,
)

data class SessionProfile(
    val index: Int,
    val protocol: SessionProtocol,
    val description: String,
)

data class SessionWarning(val code: String, val message: String)

enum class SessionProtocol { UNSPECIFIED, OUTLINE, XRAY, TRUST_TUNNEL, UNKNOWN }

sealed interface SessionStartTarget {
    data object AutoSelect : SessionStartTarget
    data class ProfileIndex(val index: Int) : SessionStartTarget
}

enum class SessionState {
    UNSPECIFIED,
    IDLE,
    CONFIGURED,
    PROBING,
    PREPARING,
    CONNECTED,
    STOPPING,
    FAILED,
    DESTROYED,
    UNKNOWN,
}

enum class SessionFailureCode {
    UNSPECIFIED,
    INVALID_ARGUMENT,
    NOT_FOUND,
    CONFLICT,
    NOT_CONFIGURED,
    STALE_GENERATION,
    UNSUPPORTED,
    MALFORMED_CONFIG,
    PROBE_FAILED,
    PLATFORM_FAILED,
    RUNTIME_FAILED,
    CANCELED,
    INTERNAL,
    CLEANUP_FAILED,
    UNKNOWN,
}

internal fun String?.toSessionFailureCode(): SessionFailureCode =
    this?.let { raw -> SessionFailureCode.entries.firstOrNull { it.name == raw } }
        ?: SessionFailureCode.UNKNOWN

data class SessionEvent(
    val generation: ULong,
    val sequence: ULong,
    val state: SessionState,
    val failureCode: SessionFailureCode? = null,
    val sessionId: String = "",
)

data class SessionSnapshot(
    val generation: ULong,
    val state: SessionState,
    val configured: Boolean,
    val cleanupComplete: Boolean,
    val lastFailureCode: SessionFailureCode? = null,
    val sessionId: String = "",
)

data class SessionObservation(val events: List<SessionEvent>, val nextSequence: ULong)
