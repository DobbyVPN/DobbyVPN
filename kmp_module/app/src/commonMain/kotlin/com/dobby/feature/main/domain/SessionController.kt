package com.dobby.feature.main.domain

import kotlinx.coroutines.flow.Flow

/** Platform boundary for the single Go-owned session. */
interface SessionController {
    suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration>
    suspend fun start(target: SessionStartTarget): SessionControllerResult<SessionStart>
    suspend fun stop(generation: ULong): SessionControllerResult<SessionStop>
    suspend fun snapshot(): SessionControllerResult<SessionSnapshot>
    fun watch(): Flow<SessionSnapshot>
    suspend fun reset(): SessionControllerResult<SessionSnapshot>
}

sealed interface SessionControllerResult<out T> {
    data class Success<T>(val value: T) : SessionControllerResult<T>
    data class Failure(
        val message: String,
        val code: SessionFailureCode,
    ) : SessionControllerResult<Nothing>
}

internal fun SessionControllerResult.Failure.asException(operation: String): IllegalStateException =
    IllegalStateException("$operation failed: ${code.name}: $message")

data class SessionConfiguration(
    val sessionId: String,
    val sequence: ULong = 0uL,
    val digest: String,
    val sourceKind: SessionSourceKind = SessionSourceKind.INLINE,
    val profiles: List<SessionProfile>,
    val warnings: List<SessionWarning>,
)

data class SessionStart(val sessionId: String, val generation: ULong, val sequence: ULong)
data class SessionStop(val sessionId: String, val generation: ULong, val sequence: ULong)

data class SessionProfile(
    val index: Int,
    val protocol: SessionProtocol,
    val description: String,
)

data class SessionWarning(val code: String, val message: String)

enum class SessionSourceKind { INLINE, URL }
enum class SessionProtocol { OUTLINE, XRAY, TRUST_TUNNEL }

sealed interface SessionStartTarget {
    data object AutoSelect : SessionStartTarget
    data class ProfileIndex(val index: Int) : SessionStartTarget
}

enum class SessionState {
    IDLE,
    CONFIGURED,
    PROBING,
    PREPARING,
    CONNECTED,
    STOPPING,
    FAILED,
}

enum class SessionFailureCode {
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
}

internal fun String.toSessionFailureCode(): SessionFailureCode =
    SessionFailureCode.entries.firstOrNull { it.name == this }
        ?: error("unsupported session failure code: $this")

data class SessionFailure(val code: SessionFailureCode, val message: String)

/** One authoritative view; intermediate revisions may be coalesced. */
data class SessionSnapshot(
    val sessionId: String,
    val sequence: ULong,
    val generation: ULong,
    val state: SessionState,
    val configured: Boolean,
    val digest: String,
    val sourceKind: SessionSourceKind,
    val profiles: List<SessionProfile>,
    val warnings: List<SessionWarning>,
    val activeProfile: SessionProfile?,
    val lastFailure: SessionFailure?,
    val cleanupComplete: Boolean,
)
