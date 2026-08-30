package interop.session

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow

/**
 * The desktop-facing sessionapi/v2 transport surface. Configuration remains
 * opaque to this layer: its bytes are sent directly to the RPC.
 */
@Suppress("TooManyFunctions")
interface SessionLibrary {
    suspend fun getCapabilities(): SessionResult<SessionCapabilities>
    suspend fun createSession(): SessionResult<String>
    suspend fun recoverActiveSession(): SessionResult<String> = SessionResult.Failure(
        SessionFailure(SessionFailureCode.NOT_FOUND, "no active session is available for recovery"),
    )
    suspend fun configure(sessionId: String, commandId: String, rawConfig: ByteArray): SessionResult<SessionConfiguration>
    suspend fun start(sessionId: String, commandId: String, target: SessionStartTarget): SessionResult<ULong>
    suspend fun stop(sessionId: String, commandId: String, generation: ULong): SessionResult<ULong>
    suspend fun snapshot(sessionId: String): SessionResult<SessionSnapshot>
    suspend fun observe(sessionId: String, afterSequence: ULong): SessionResult<SessionObservation>
    /** Push stream of ordered events; the default keeps test doubles source-compatible. */
    fun watch(sessionId: String, afterSequence: ULong): Flow<SessionEvent> = emptyFlow()
    suspend fun destroySession(sessionId: String): SessionResult<Unit>
}

data class SessionCapabilities(
    val version: String,
    val protocols: List<SessionProtocol>,
    val features: List<SessionFeature>,
)

data class SessionFeature(val name: String, val enabled: Boolean)

enum class SessionProtocol { UNSPECIFIED, OUTLINE, XRAY, TRUST_TUNNEL, UNKNOWN }

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

data class SessionFailure(val code: SessionFailureCode, val message: String)

sealed interface SessionResult<out T> {
    data class Success<T>(val value: T) : SessionResult<T>
    data class Failure(val failure: SessionFailure) : SessionResult<Nothing>
}

data class SessionProfile(
    val index: Int,
    val protocol: SessionProtocol,
    val description: String,
)

data class SessionWarning(val code: String, val message: String)

data class SessionConfiguration(
    val digest: String,
    val profiles: List<SessionProfile>,
    val warnings: List<SessionWarning>,
    val sourceKind: SessionSourceKind = SessionSourceKind.INLINE,
)

enum class SessionSourceKind { UNSPECIFIED, INLINE, URL }

sealed interface SessionStartTarget {
    data object AutoSelect : SessionStartTarget
    data class ProfileIndex(val index: Int) : SessionStartTarget
}

data class SessionEvent(
    val sessionId: String,
    val generation: ULong,
    val sequence: ULong,
    val state: SessionState,
    val profile: SessionProfile?,
    val failure: SessionFailure?,
    val warning: SessionWarning?,
)

data class SessionSnapshot(
    val sessionId: String,
    val generation: ULong,
    val state: SessionState,
    val configured: Boolean,
    val activeProfile: SessionProfile?,
    val lastFailure: SessionFailure?,
    val cleanupComplete: Boolean,
)

data class SessionObservation(val events: List<SessionEvent>, val nextSequence: ULong)
