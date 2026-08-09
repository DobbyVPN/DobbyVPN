package interop.session

/**
 * The desktop-facing sessionapi/v1 transport surface. Configuration remains
 * opaque to this layer: its bytes are sent directly to the RPC.
 */
interface SessionLibrary {
    suspend fun getCapabilities(): SessionResult<SessionCapabilities>
    suspend fun createSession(): SessionResult<String>
    suspend fun configure(sessionId: String, commandId: String, rawConfig: ByteArray): SessionResult<SessionConfiguration>
    suspend fun start(sessionId: String, commandId: String, target: SessionStartTarget): SessionResult<ULong>
    suspend fun stop(sessionId: String, commandId: String, generation: ULong): SessionResult<ULong>
    suspend fun snapshot(sessionId: String): SessionResult<SessionSnapshot>
    suspend fun observe(sessionId: String, afterSequence: ULong): SessionResult<SessionObservation>
    suspend fun destroySession(sessionId: String): SessionResult<Unit>
}

data class SessionCapabilities(
    val version: String,
    val protocols: List<SessionProtocol>,
    val features: List<SessionFeature>,
    val telemetryNetworkDisabled: Boolean,
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
)

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
