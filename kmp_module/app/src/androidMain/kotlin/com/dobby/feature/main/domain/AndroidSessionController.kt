package com.dobby.feature.main.domain

import android.content.Context
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.PlatformServiceRegistry
import com.dobby.gomobile.dobbyvpn.Dobbyvpn
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.onStart
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonObject

/** Android gomobile adapter for the process-owned Go session. */
internal class AndroidSessionController(
    private val context: Context,
    private val connectionState: ConnectionStateRepository = ConnectionStateRepository(),
) : SessionController {
    private val mutex = Mutex()

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> SessionEnvelopeDecoder.decode(
                    Dobbyvpn.configureSession(current.value.sessionId, current.value.sequence.toLong(), rawConfig),
                ) { it.toSessionConfiguration(current.value.sessionId) }
            }
        }
    }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<SessionStart> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> {
                    val snapshot = current.value
                    DobbyVpnService.requestShell(context, snapshot.sessionId)
                    if (!PlatformServiceRegistry.awaitReady(5_000)) {
                        return@withLock SessionControllerResult.Failure(
                            message = "Android VPN service did not become ready",
                            code = SessionFailureCode.PLATFORM_FAILED,
                        )
                    }
                    val mode = if (target is SessionStartTarget.AutoSelect) "AUTO_SELECT" else "PROFILE_INDEX"
                    val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
                    SessionEnvelopeDecoder.decode(
                        Dobbyvpn.startSession(snapshot.sessionId, snapshot.sequence.toLong(), mode, index),
                    ) {
                        SessionStart(
                            sessionId = snapshot.sessionId,
                            generation = it.requiredPositiveSessionLong("generation").toULong(),
                            sequence = it.requiredNonNegativeSessionLong("sequence").toULong(),
                        )
                    }
                }
            }
        }
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<SessionStop> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> SessionEnvelopeDecoder.decode(
                    Dobbyvpn.stopSession(current.value.sessionId, generation.toLong()),
                ) {
                    SessionStop(
                        sessionId = current.value.sessionId,
                        generation = it.requiredPositiveSessionLong("generation").toULong(),
                        sequence = it.requiredNonNegativeSessionLong("sequence").toULong(),
                    )
                }
            }
        }
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> = onWorker {
        mutex.withLock { snapshotNow() }
    }

    override fun watch(): Flow<SessionSnapshot> = connectionState.sessionChanges
        .onStart { emit(Unit) }
        .map {
            when (val current = snapshot()) {
                is SessionControllerResult.Success -> current.value
                is SessionControllerResult.Failure -> throw current.asException("session snapshot")
            }
        }

    override suspend fun reset(): SessionControllerResult<SessionSnapshot> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> SessionEnvelopeDecoder.decode(
                    Dobbyvpn.resetSession(current.value.sessionId, current.value.sequence.toLong()),
                    JsonObject::toSessionSnapshot,
                )
            }
        }
    }

    private fun snapshotNow(): SessionControllerResult<SessionSnapshot> =
        SessionEnvelopeDecoder.decode(Dobbyvpn.snapshotSession(""), JsonObject::toSessionSnapshot)

    private suspend fun <T> onWorker(block: suspend () -> T): T = withContext(Dispatchers.Default) { block() }
}
