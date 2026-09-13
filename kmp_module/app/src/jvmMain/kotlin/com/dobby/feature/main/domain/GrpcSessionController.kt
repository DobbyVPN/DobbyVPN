package com.dobby.feature.main.domain

import com.dobby.grpcproto.SessionFailureCode as ProtoFailureCode
import com.dobby.grpcproto.SessionProtocol as ProtoProtocol
import com.dobby.grpcproto.SessionSnapshot as ProtoSnapshot
import com.dobby.grpcproto.SessionSourceKind as ProtoSourceKind
import com.dobby.grpcproto.SessionStartMode
import com.dobby.grpcproto.SessionState as ProtoState
import interop.session.SessionGrpcLibrary
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/** Desktop adapter; protobuf is mapped directly into the small shared UI model. */
internal class GrpcSessionController(
    private val session: SessionGrpcLibrary,
) : SessionController {
    private val mutex = Mutex()

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> = onWorker {
        request {
            mutex.withLock {
                when (val current = snapshotNow()) {
                    is SessionControllerResult.Failure -> current
                    is SessionControllerResult.Success -> {
                        val snapshot = current.value
                        val response = session.configure(snapshot.sessionId, snapshot.sequence.toLong(), rawConfig)
                        response.result(response.hasFailure(), response.failure) {
                            SessionConfiguration(
                                sessionId = snapshot.sessionId,
                                sequence = response.sequence.toULongChecked(),
                                digest = response.digest,
                                sourceKind = response.sourceKind.toDomain(),
                                profiles = response.profilesList.map { it.toDomain() },
                                warnings = response.warningsList.map { it.toDomain() },
                            )
                        }
                    }
                }
            }
        }
    }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<SessionStart> = onWorker {
        request {
            mutex.withLock {
                when (val current = snapshotNow()) {
                    is SessionControllerResult.Failure -> current
                    is SessionControllerResult.Success -> {
                        val snapshot = current.value
                        val mode = when (target) {
                            SessionStartTarget.AutoSelect -> SessionStartMode.SESSION_START_MODE_AUTO_SELECT
                            is SessionStartTarget.ProfileIndex -> SessionStartMode.SESSION_START_MODE_PROFILE_INDEX
                        }
                        val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
                        val response = session.start(snapshot.sessionId, snapshot.sequence.toLong(), mode, index)
                        response.result(response.hasFailure(), response.failure) {
                            SessionStart(
                                sessionId = snapshot.sessionId,
                                generation = response.generation.toULongChecked(positive = true),
                                sequence = response.sequence.toULongChecked(),
                            )
                        }
                    }
                }
            }
        }
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<SessionStop> = onWorker {
        request {
            mutex.withLock {
                when (val current = snapshotNow()) {
                    is SessionControllerResult.Failure -> current
                    is SessionControllerResult.Success -> {
                        val response = session.stop(current.value.sessionId, generation.toLong())
                        response.result(response.hasFailure(), response.failure) {
                            SessionStop(
                                sessionId = current.value.sessionId,
                                generation = response.generation.toULongChecked(positive = true),
                                sequence = response.sequence.toULongChecked(),
                            )
                        }
                    }
                }
            }
        }
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> = onWorker {
        request { snapshotNow() }
    }

    override fun watch(): Flow<SessionSnapshot> = session.watch().map(ProtoSnapshot::toDomain)

    override suspend fun reset(): SessionControllerResult<SessionSnapshot> = onWorker {
        request {
            mutex.withLock {
                when (val current = snapshotNow()) {
                    is SessionControllerResult.Failure -> current
                    is SessionControllerResult.Success -> {
                        val response = session.reset(current.value.sessionId, current.value.sequence.toLong())
                        response.result(response.hasFailure(), response.failure) { response.snapshot.toDomain() }
                    }
                }
            }
        }
    }

    private suspend fun snapshotNow(): SessionControllerResult<SessionSnapshot> {
        val response = session.snapshot()
        return response.result(response.hasFailure(), response.failure) { response.snapshot.toDomain() }
    }

    private suspend fun <T> onWorker(block: suspend () -> T): T = withContext(Dispatchers.IO) { block() }

    private suspend fun <T> request(block: suspend () -> SessionControllerResult<T>): SessionControllerResult<T> =
        try {
            block()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            SessionControllerResult.Failure("Desktop session service request failed", SessionFailureCode.INTERNAL)
        }
}

private fun <T, R> T.result(
    hasFailure: Boolean,
    failure: com.dobby.grpcproto.SessionFailure,
    value: () -> R,
): SessionControllerResult<R> = if (hasFailure) {
    SessionControllerResult.Failure(failure.message, failure.code.toDomain())
} else {
    SessionControllerResult.Success(value())
}

private fun ProtoSnapshot.toDomain() = SessionSnapshot(
    sessionId = sessionId,
    sequence = sequence.toULongChecked(),
    generation = generation.toULongChecked(),
    state = state.toDomain(),
    configured = configured,
    digest = digest,
    sourceKind = sourceKind.toDomain(),
    profiles = profilesList.map { it.toDomain() },
    warnings = warningsList.map { it.toDomain() },
    activeProfile = if (hasActiveProfile()) activeProfile.toDomain() else null,
    lastFailure = if (hasLastFailure()) SessionFailure(lastFailure.code.toDomain(), lastFailure.message) else null,
    cleanupComplete = cleanupComplete,
)

private fun com.dobby.grpcproto.SessionProfile.toDomain() =
    SessionProfile(index, protocol.toDomain(), description)

private fun com.dobby.grpcproto.SessionWarning.toDomain() = SessionWarning(code, message)

private fun ProtoProtocol.toDomain() = when (this) {
    ProtoProtocol.SESSION_PROTOCOL_OUTLINE -> SessionProtocol.OUTLINE
    ProtoProtocol.SESSION_PROTOCOL_XRAY -> SessionProtocol.XRAY
    ProtoProtocol.SESSION_PROTOCOL_TRUST_TUNNEL -> SessionProtocol.TRUST_TUNNEL
    ProtoProtocol.SESSION_PROTOCOL_UNSPECIFIED, ProtoProtocol.UNRECOGNIZED -> error("session protocol is unspecified")
}

private fun ProtoSourceKind.toDomain() = when (this) {
    ProtoSourceKind.SESSION_SOURCE_KIND_URL -> SessionSourceKind.URL
    ProtoSourceKind.SESSION_SOURCE_KIND_INLINE, ProtoSourceKind.SESSION_SOURCE_KIND_UNSPECIFIED -> SessionSourceKind.INLINE
    ProtoSourceKind.UNRECOGNIZED -> error("session source kind is unrecognized")
}

private fun ProtoState.toDomain() = when (this) {
    ProtoState.SESSION_STATE_IDLE -> SessionState.IDLE
    ProtoState.SESSION_STATE_CONFIGURED -> SessionState.CONFIGURED
    ProtoState.SESSION_STATE_PROBING -> SessionState.PROBING
    ProtoState.SESSION_STATE_PREPARING -> SessionState.PREPARING
    ProtoState.SESSION_STATE_CONNECTED -> SessionState.CONNECTED
    ProtoState.SESSION_STATE_STOPPING -> SessionState.STOPPING
    ProtoState.SESSION_STATE_FAILED -> SessionState.FAILED
    ProtoState.SESSION_STATE_UNSPECIFIED, ProtoState.UNRECOGNIZED -> error("session state is unspecified")
}

private fun ProtoFailureCode.toDomain() = when (this) {
    ProtoFailureCode.SESSION_FAILURE_CODE_INVALID_ARGUMENT -> SessionFailureCode.INVALID_ARGUMENT
    ProtoFailureCode.SESSION_FAILURE_CODE_NOT_FOUND -> SessionFailureCode.NOT_FOUND
    ProtoFailureCode.SESSION_FAILURE_CODE_CONFLICT -> SessionFailureCode.CONFLICT
    ProtoFailureCode.SESSION_FAILURE_CODE_NOT_CONFIGURED -> SessionFailureCode.NOT_CONFIGURED
    ProtoFailureCode.SESSION_FAILURE_CODE_STALE_GENERATION -> SessionFailureCode.STALE_GENERATION
    ProtoFailureCode.SESSION_FAILURE_CODE_UNSUPPORTED -> SessionFailureCode.UNSUPPORTED
    ProtoFailureCode.SESSION_FAILURE_CODE_MALFORMED_CONFIG -> SessionFailureCode.MALFORMED_CONFIG
    ProtoFailureCode.SESSION_FAILURE_CODE_PROBE_FAILED -> SessionFailureCode.PROBE_FAILED
    ProtoFailureCode.SESSION_FAILURE_CODE_PLATFORM_FAILED -> SessionFailureCode.PLATFORM_FAILED
    ProtoFailureCode.SESSION_FAILURE_CODE_RUNTIME_FAILED -> SessionFailureCode.RUNTIME_FAILED
    ProtoFailureCode.SESSION_FAILURE_CODE_CANCELED -> SessionFailureCode.CANCELED
    ProtoFailureCode.SESSION_FAILURE_CODE_INTERNAL -> SessionFailureCode.INTERNAL
    ProtoFailureCode.SESSION_FAILURE_CODE_CLEANUP_FAILED -> SessionFailureCode.CLEANUP_FAILED
    ProtoFailureCode.SESSION_FAILURE_CODE_UNSPECIFIED, ProtoFailureCode.UNRECOGNIZED -> error("session failure code is unspecified")
}

private fun Long.toULongChecked(positive: Boolean = false): ULong {
    require(if (positive) this > 0L else this >= 0L)
    return toULong()
}
