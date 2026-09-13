package com.dobby.feature.main.domain

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonObject

/** App-to-provider bridge; the provider owns the Go process and session. */
interface IosSessionBridge {
    fun configure(sessionID: String, expectedSequence: Long, rawConfig: ByteArray): String
    fun start(sessionID: String, expectedSequence: Long, mode: String, index: Int): String
    fun stop(sessionID: String, generation: Long): String
    fun snapshot(sessionID: String): String
    fun reset(sessionID: String, expectedSequence: Long): String
    /** Blocks until a cross-process state-change hint arrives or the wait is canceled. */
    fun awaitEvent(timeoutMillis: Long): Boolean
}

internal class IosSessionController(
    private val bridge: IosSessionBridge,
) : SessionController {
    private val mutex = Mutex()

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> decode {
                    SessionEnvelopeDecoder.decode(
                        bridge.configure(current.value.sessionId, current.value.sequence.toLong(), rawConfig),
                    ) { it.toSessionConfiguration(current.value.sessionId) }
                }
            }
        }
    }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<SessionStart> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> {
                    val mode = if (target is SessionStartTarget.AutoSelect) "AUTO_SELECT" else "PROFILE_INDEX"
                    val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
                    decode {
                        SessionEnvelopeDecoder.decode(
                            bridge.start(current.value.sessionId, current.value.sequence.toLong(), mode, index),
                        ) {
                            SessionStart(
                                sessionId = current.value.sessionId,
                                generation = it.requiredPositiveSessionLong("generation").toULong(),
                                sequence = it.requiredNonNegativeSessionLong("sequence").toULong(),
                            )
                        }
                    }
                }
            }
        }
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<SessionStop> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> decode {
                    SessionEnvelopeDecoder.decode(bridge.stop(current.value.sessionId, generation.toLong())) {
                        SessionStop(
                            sessionId = current.value.sessionId,
                            generation = it.requiredPositiveSessionLong("generation").toULong(),
                            sequence = it.requiredNonNegativeSessionLong("sequence").toULong(),
                        )
                    }
                }
            }
        }
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> = onWorker { snapshotNow() }

    override fun watch(): Flow<SessionSnapshot> = flow {
        var first = true
        while (currentCoroutineContext().isActive) {
            if (!first) bridge.awaitEvent(timeoutMillis = 5_000)
            first = false
            when (val current = snapshot()) {
                is SessionControllerResult.Success -> emit(current.value)
                is SessionControllerResult.Failure -> throw current.asException("session snapshot")
            }
        }
    }

    override suspend fun reset(): SessionControllerResult<SessionSnapshot> = onWorker {
        mutex.withLock {
            when (val current = snapshotNow()) {
                is SessionControllerResult.Failure -> current
                is SessionControllerResult.Success -> decode {
                    SessionEnvelopeDecoder.decode(
                        bridge.reset(current.value.sessionId, current.value.sequence.toLong()),
                        JsonObject::toSessionSnapshot,
                    )
                }
            }
        }
    }

    private suspend fun snapshotNow(): SessionControllerResult<SessionSnapshot> = decode {
        SessionEnvelopeDecoder.decode(bridge.snapshot(""), JsonObject::toSessionSnapshot)
    }

    private suspend fun <T> onWorker(block: suspend () -> T): T = withContext(Dispatchers.Default) { block() }

    private inline fun <T> decode(block: () -> SessionControllerResult<T>): SessionControllerResult<T> = try {
        block()
    } catch (_: Exception) {
        SessionControllerResult.Failure("iOS session provider request failed", SessionFailureCode.INTERNAL)
    }
}
