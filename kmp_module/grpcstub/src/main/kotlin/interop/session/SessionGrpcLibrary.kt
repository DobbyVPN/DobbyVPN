package interop.session

import com.dobby.grpcproto.SessionConfigureRequest
import com.dobby.grpcproto.SessionCreateSessionRequest
import com.dobby.grpcproto.SessionDestroySessionRequest
import com.dobby.grpcproto.SessionFailure as ProtoFailure
import com.dobby.grpcproto.SessionFeature as ProtoFeature
import com.dobby.grpcproto.SessionGetCapabilitiesRequest
import com.dobby.grpcproto.SessionObserveRequest
import com.dobby.grpcproto.SessionProfile as ProtoProfile
import com.dobby.grpcproto.SessionProtocol as ProtoProtocol
import com.dobby.grpcproto.SessionSnapshot as ProtoSnapshot
import com.dobby.grpcproto.SessionSnapshotRequest
import com.dobby.grpcproto.SessionStartMode
import com.dobby.grpcproto.SessionStartRequest
import com.dobby.grpcproto.SessionState as ProtoState
import com.dobby.grpcproto.SessionStopRequest
import com.dobby.grpcproto.SessionWarning as ProtoWarning
import com.dobby.grpcproto.VpnGrpcKt
import com.google.protobuf.ByteString
import io.grpc.Channel
import io.grpc.StatusException

/**
 * Thin sessionapi/v1 RPC client for desktop callers. It performs no TOML
 * parsing, decoding, or logging, so configuration text and credentials stay out of this
 * layer's errors and diagnostics.
 */
open class SessionGrpcLibrary(channel: Channel) : SessionLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override suspend fun getCapabilities(): SessionResult<SessionCapabilities> = try {
        SessionResult.Success(stub.getCapabilities(SessionGetCapabilitiesRequest.getDefaultInstance()).toTransport())
    } catch (_: StatusException) {
        unavailable()
    }

    override suspend fun createSession(): SessionResult<String> = try {
        val response = stub.createSession(SessionCreateSessionRequest.getDefaultInstance())
        response.result { response.sessionId }
    } catch (_: StatusException) {
        unavailable()
    }

    override suspend fun configure(
        sessionId: String,
        commandId: String,
        rawConfig: ByteArray,
    ): SessionResult<SessionConfiguration> = try {
        val response = stub.configure(SessionRequests.configure(sessionId, commandId, rawConfig))
        response.result {
            SessionConfiguration(
                digest = response.digest,
                profiles = response.profilesList.map(ProtoProfile::toTransport),
                warnings = response.warningsList.map(ProtoWarning::toTransport),
            )
        }
    } catch (_: StatusException) {
        unavailable()
    }

    override suspend fun start(
        sessionId: String,
        commandId: String,
        target: SessionStartTarget,
    ): SessionResult<ULong> = try {
        val response = stub.start(SessionRequests.start(sessionId, commandId, target))
        response.result { response.generation.toULong() }
    } catch (_: StatusException) {
        unavailable()
    }

    override suspend fun stop(
        sessionId: String,
        commandId: String,
        generation: ULong,
    ): SessionResult<ULong> = try {
        val response = stub.stop(SessionRequests.stop(sessionId, commandId, generation))
        response.result { response.generation.toULong() }
    } catch (_: StatusException) {
        unavailable()
    }

    override suspend fun snapshot(sessionId: String): SessionResult<SessionSnapshot> = try {
        val response = stub.snapshot(SessionSnapshotRequest.newBuilder().setSessionId(sessionId).build())
        response.result { response.snapshot.toTransport() }
    } catch (_: StatusException) {
        unavailable()
    }

    override suspend fun observe(sessionId: String, afterSequence: ULong): SessionResult<SessionObservation> = try {
        val response = stub.observe(
            SessionObserveRequest.newBuilder()
                .setSessionId(sessionId)
                .setAfterSequence(afterSequence.toLong())
                .build(),
        )
        response.result {
            SessionObservation(
                events = response.eventsList.map { it.toTransport() },
                nextSequence = response.nextSequence.toULong(),
            )
        }
    } catch (_: StatusException) {
        unavailable()
    }

    override suspend fun destroySession(sessionId: String): SessionResult<Unit> = try {
        val response = stub.destroySession(
            SessionDestroySessionRequest.newBuilder().setSessionId(sessionId).build(),
        )
        response.result { Unit }
    } catch (_: StatusException) {
        unavailable()
    }
}

internal object SessionRequests {
    fun configure(sessionId: String, commandId: String, rawConfig: ByteArray): SessionConfigureRequest =
        SessionConfigureRequest.newBuilder()
            .setSessionId(sessionId)
            .setCommandId(commandId)
            .setRawConfig(ByteString.copyFrom(rawConfig))
            .build()

    fun start(sessionId: String, commandId: String, target: SessionStartTarget): SessionStartRequest =
        SessionStartRequest.newBuilder()
            .setSessionId(sessionId)
            .setCommandId(commandId)
            .apply {
                when (target) {
                    SessionStartTarget.AutoSelect -> setMode(SessionStartMode.SESSION_START_MODE_AUTO_SELECT)
                    is SessionStartTarget.ProfileIndex -> {
                        setMode(SessionStartMode.SESSION_START_MODE_PROFILE_INDEX)
                        setProfileIndex(target.index)
                    }
                }
            }
            .build()

    fun stop(sessionId: String, commandId: String, generation: ULong): SessionStopRequest =
        SessionStopRequest.newBuilder()
            .setSessionId(sessionId)
            .setCommandId(commandId)
            .setGeneration(generation.toLong())
            .build()
}

private fun <T> result(hasFailure: Boolean, failure: ProtoFailure, value: () -> T): SessionResult<T> =
    if (hasFailure) SessionResult.Failure(failure.toTransport()) else SessionResult.Success(value())

private fun unavailable(): SessionResult.Failure =
    SessionResult.Failure(SessionFailure(SessionFailureCode.INTERNAL, "session service request failed"))

private fun com.dobby.grpcproto.SessionCreateSessionResponse.result(value: () -> String) =
    result(hasFailure(), failure, value)

private fun com.dobby.grpcproto.SessionConfigureResponse.result(value: () -> SessionConfiguration) =
    result(hasFailure(), failure, value)

private fun com.dobby.grpcproto.SessionStartResponse.result(value: () -> ULong) =
    result(hasFailure(), failure, value)

private fun com.dobby.grpcproto.SessionStopResponse.result(value: () -> ULong) =
    result(hasFailure(), failure, value)

private fun com.dobby.grpcproto.SessionSnapshotResponse.result(value: () -> SessionSnapshot) =
    result(hasFailure(), failure, value)

private fun com.dobby.grpcproto.SessionObserveResponse.result(value: () -> SessionObservation) =
    result(hasFailure(), failure, value)

private fun com.dobby.grpcproto.SessionDestroySessionResponse.result(value: () -> Unit) =
    result(hasFailure(), failure, value)

internal fun ProtoFailure.toTransport() = SessionFailure(code.toTransport(), message)

internal fun ProtoProfile.toTransport() = SessionProfile(index, protocol.toTransport(), description)

internal fun ProtoWarning.toTransport() = SessionWarning(code, message)

private fun ProtoFeature.toTransport() = SessionFeature(name, enabled)

private fun com.dobby.grpcproto.SessionGetCapabilitiesResponse.toTransport() = SessionCapabilities(
    version = version,
    protocols = protocolsList.map(ProtoProtocol::toTransport),
    features = featuresList.map(ProtoFeature::toTransport),
    telemetryNetworkDisabled = telemetryNetworkDisabled,
)

internal fun com.dobby.grpcproto.SessionEvent.toTransport() = SessionEvent(
    sessionId = sessionId,
    generation = generation.toULong(),
    sequence = sequence.toULong(),
    state = state.toTransport(),
    profile = if (hasProfile()) profile.toTransport() else null,
    failure = if (hasFailure()) failure.toTransport() else null,
    warning = if (hasWarning()) warning.toTransport() else null,
)

private fun ProtoSnapshot.toTransport() = SessionSnapshot(
    sessionId = sessionId,
    generation = generation.toULong(),
    state = state.toTransport(),
    configured = configured,
    activeProfile = if (hasActiveProfile()) activeProfile.toTransport() else null,
    lastFailure = if (hasLastFailure()) lastFailure.toTransport() else null,
    cleanupComplete = cleanupComplete,
)

private fun ProtoProtocol.toTransport() = when (this) {
    ProtoProtocol.SESSION_PROTOCOL_UNSPECIFIED -> SessionProtocol.UNSPECIFIED
    ProtoProtocol.SESSION_PROTOCOL_OUTLINE -> SessionProtocol.OUTLINE
    ProtoProtocol.SESSION_PROTOCOL_XRAY -> SessionProtocol.XRAY
    ProtoProtocol.SESSION_PROTOCOL_TRUST_TUNNEL -> SessionProtocol.TRUST_TUNNEL
    ProtoProtocol.UNRECOGNIZED -> SessionProtocol.UNKNOWN
}

private fun ProtoState.toTransport() = when (this) {
    ProtoState.SESSION_STATE_UNSPECIFIED -> SessionState.UNSPECIFIED
    ProtoState.SESSION_STATE_IDLE -> SessionState.IDLE
    ProtoState.SESSION_STATE_CONFIGURED -> SessionState.CONFIGURED
    ProtoState.SESSION_STATE_PROBING -> SessionState.PROBING
    ProtoState.SESSION_STATE_PREPARING -> SessionState.PREPARING
    ProtoState.SESSION_STATE_CONNECTED -> SessionState.CONNECTED
    ProtoState.SESSION_STATE_STOPPING -> SessionState.STOPPING
    ProtoState.SESSION_STATE_FAILED -> SessionState.FAILED
    ProtoState.SESSION_STATE_DESTROYED -> SessionState.DESTROYED
    ProtoState.UNRECOGNIZED -> SessionState.UNKNOWN
}

private fun com.dobby.grpcproto.SessionFailureCode.toTransport() = when (this) {
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_UNSPECIFIED -> SessionFailureCode.UNSPECIFIED
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_INVALID_ARGUMENT -> SessionFailureCode.INVALID_ARGUMENT
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_NOT_FOUND -> SessionFailureCode.NOT_FOUND
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_CONFLICT -> SessionFailureCode.CONFLICT
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_NOT_CONFIGURED -> SessionFailureCode.NOT_CONFIGURED
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_STALE_GENERATION -> SessionFailureCode.STALE_GENERATION
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_UNSUPPORTED -> SessionFailureCode.UNSUPPORTED
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_MALFORMED_CONFIG -> SessionFailureCode.MALFORMED_CONFIG
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_PROBE_FAILED -> SessionFailureCode.PROBE_FAILED
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_PLATFORM_FAILED -> SessionFailureCode.PLATFORM_FAILED
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_RUNTIME_FAILED -> SessionFailureCode.RUNTIME_FAILED
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_CANCELED -> SessionFailureCode.CANCELED
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_INTERNAL -> SessionFailureCode.INTERNAL
    com.dobby.grpcproto.SessionFailureCode.SESSION_FAILURE_CODE_CLEANUP_FAILED -> SessionFailureCode.CLEANUP_FAILED
    com.dobby.grpcproto.SessionFailureCode.UNRECOGNIZED -> SessionFailureCode.UNKNOWN
}
