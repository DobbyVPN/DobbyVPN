package com.dobby.feature.main.domain

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlin.random.Random

/**
 * Narrow app/NetworkExtension handoff. Swift stores configuration in its
 * encrypted one-shot mailbox and sends only authenticated fixed commands to
 * the provider. All returned JSON is the opaque Go envelope.
 */
interface IosSessionBridge {
    fun recover(commandID: String): String
    fun create(commandID: String): String
    fun configure(sessionID: String, commandID: String, rawConfig: ByteArray): String
    fun start(sessionID: String, commandID: String, mode: String, index: Int): String
    fun stop(sessionID: String, commandID: String, generation: Long): String
    fun snapshot(sessionID: String): String
    fun observe(sessionID: String, afterSequence: Long): String
    fun destroy(sessionID: String): String
    /** Blocks on the cross-process content-free event wake; no state payload. */
    fun awaitEvent(timeoutMillis: Long): Boolean
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

/**
 * KMP owns only the ephemeral Go session handle. Go remains the sole
 * configuration, generation, state, event, and cleanup authority.
 */
internal class IosSessionController(
    private val bridge: IosSessionBridge,
) : SessionController {
    private companion object {
        // A timeout is only a bounded cancellation/wakeup chunk. It must not
        // cause another Observe: the next Observe is permitted only after a
        // real Darwin wake, preventing steady polling of the provider.
        const val eventWaitChunkMillis = 5_000L
    }

    private val mutex = Mutex()
    private var sessionID: String? = null
    private var sessionIdentityChanged = false

    override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> =
        onWorker {
            mutex.withLock {
                when (val session = recoverOrCreate()) {
                    is SessionControllerResult.Failure -> preserveFailure(session)
                    is SessionControllerResult.Success -> {
                        // One command ID is created for this logical operation
                        // and reused by Swift for every provider retry.
                        val commandID = commandID("configure")
                        SessionEnvelopeDecoder.decode(bridge.configure(session.value, commandID, rawConfig)) {
                            it.toSessionConfiguration()
                        }
                    }
                }
            }
        }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> =
        onWorker {
            mutex.withLock {
                when (val session = recoverOrCreate()) {
                    is SessionControllerResult.Failure -> preserveFailure(session)
                    is SessionControllerResult.Success -> {
                        val mode = if (target is SessionStartTarget.AutoSelect) "AUTO_SELECT" else "PROFILE_INDEX"
                        val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
                        SessionEnvelopeDecoder.decode(
                            bridge.start(session.value, commandID("start"), mode, index),
                        ) { it.requiredPositiveSessionLong("generation").toULong() }
                    }
                }
            }
        }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> =
        onWorker {
            mutex.withLock {
                when (val session = recoverOnly()) {
                    is SessionControllerResult.Failure -> preserveFailure(session)
                    is SessionControllerResult.Success -> SessionEnvelopeDecoder.decode(
                        bridge.stop(session.value, commandID("stop"), generation.toLong()),
                    ) { it.requiredPositiveSessionLong("generation").toULong() }
                }
            }
        }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> =
        onWorker {
            mutex.withLock {
                when (val session = recoverOnly()) {
                    is SessionControllerResult.Failure -> preserveFailure(session)
                    is SessionControllerResult.Success -> SessionEnvelopeDecoder.decode(bridge.snapshot(session.value)) {
                        it.toSessionSnapshot()
                    }
                }
            }
        }

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> =
        observeAt(afterSequence).first

    /**
     * The provider callback is content-free, so the app fetches the retained
     * ordered ledger through Observe. The wait is a cross-process wake, not a
     * polling timer, and all bridge work runs off the UI dispatcher.
     */
    override fun watch(afterSequence: ULong): Flow<SessionEvent> = flow {
        var cursor = afterSequence
        var firstObserve = true
        while (currentCoroutineContext().isActive) {
            if (!firstObserve) {
                val signaled = withContext(Dispatchers.Default) {
                    bridge.awaitEvent(timeoutMillis = eventWaitChunkMillis)
                }
                if (!signaled) continue
            }
            firstObserve = false
            val (observed, resetForNewSession) = observeAt(cursor)
            if (resetForNewSession) cursor = 0uL
            when (observed) {
                is SessionControllerResult.Success -> {
                    val ordered = observed.value.events.sortedBy { it.sequence }
                    val contiguous = mutableListOf<SessionEvent>()
                    var expected = cursor + 1uL
                    var gap = false
                    ordered.forEach { event ->
                        if (gap) return@forEach
                        when {
                            event.sequence < expected -> Unit // duplicate/replayed ledger entry
                            event.sequence > expected -> gap = true
                            else -> {
                                contiguous += event
                                expected = event.sequence + 1uL
                            }
                        }
                    }
                    if (!gap && observed.value.nextSequence >= expected) gap = true
                    if (gap) {
                        // Do not infer state or jump over missing events. Ending this
                        // collection makes the shared UI reconcile a Go snapshot before
                        // it resubscribes to the retained ledger.
                        return@flow
                    }
                    contiguous.forEach { event ->
                        emit(event)
                        cursor = event.sequence
                    }
                }
                is SessionControllerResult.Failure -> {
                    if (observed.code == SessionFailureCode.NOT_FOUND) {
                        // There is no Go session to observe yet (or the old
                        // provider process was replaced). Stay on the Darwin
                        // wake channel instead of ending the flow and making
                        // MainViewModel restart it on a short timer. Configure
                        // or Create will publish a real event and wake this
                        // collector for its next Observe.
                        continue
                    }
                    // Let the shared UI reconcile a snapshot and decide whether
                    // to resubscribe. Do not hide a typed provider failure in a
                    // timer loop.
                    return@flow
                }
            }
            // Darwin notification is only a wake. The next Observe reads Go's
            // complete ordered ledger and closes the race between the initial
            // backfill and event subscription.
        }
    }

    override suspend fun destroy(): SessionControllerResult<Unit> =
        onWorker {
            mutex.withLock {
                // A restarted UI may have lost only its ephemeral handle. Recover
                // the Go-owned session, but never create one merely to destroy it.
                when (val session = recoverOnly()) {
                    is SessionControllerResult.Failure -> if (session.code == SessionFailureCode.NOT_FOUND) {
                        SessionControllerResult.Success(Unit)
                    } else preserveFailure(session)
                    is SessionControllerResult.Success -> {
                        val result = SessionEnvelopeDecoder.decode(bridge.destroy(session.value)) { Unit }
                        if (result is SessionControllerResult.Success) sessionID = null
                        result
                    }
                }
            }
        }

    private suspend fun <T> onWorker(block: suspend () -> T): T = withContext(Dispatchers.Default) { block() }

    private suspend fun observeAt(afterSequence: ULong): Pair<SessionControllerResult<SessionObservation>, Boolean> =
        onWorker {
            mutex.withLock {
                when (val session = recoverOnly()) {
                    is SessionControllerResult.Failure -> preserveFailure(session) to false
                    is SessionControllerResult.Success -> {
                        val reset = sessionIdentityChanged
                        val cursor = if (reset) 0uL else afterSequence
                        val result = SessionEnvelopeDecoder.decode(
                            bridge.observe(session.value, cursor.toLong()),
                        ) { it.toSessionObservation() }
                        // Retain the reset marker across a failed Observe. A
                        // transport/authentication failure must not cause the
                        // next attempt to send an old session's high cursor to
                        // a newly recovered Go session.
                        if (result is SessionControllerResult.Success) sessionIdentityChanged = false
                        result to reset
                    }
                }
            }
        }

    private fun adoptSession(id: String) {
        if (sessionID != null && sessionID != id) sessionIdentityChanged = true
        sessionID = id
    }

    private fun forgetSessionAfterNotFound() {
        if (sessionID != null) sessionIdentityChanged = true
        sessionID = null
    }

    private fun recoverOrCreate(): SessionControllerResult<String> {
        // The containing app may outlive the NetworkExtension process. Always
        // ask the current Go manager to recover first; a cached ID is only an
        // ephemeral hint and must never bypass process-restart recovery.
        val recovered = SessionEnvelopeDecoder.decode(
            bridge.recover(commandID("recover")),
        ) { it.requiredSessionID() }
        when (recovered) {
            is SessionControllerResult.Success -> return recovered.also { adoptSession(it.value) }
            is SessionControllerResult.Failure -> if (recovered.code != SessionFailureCode.NOT_FOUND) {
                return preserveFailure(recovered)
            } else forgetSessionAfterNotFound()
        }
        val created = SessionEnvelopeDecoder.decode(
            bridge.create(commandID("create")),
        ) { it.requiredSessionID() }
        return when (created) {
            is SessionControllerResult.Success -> created.also { adoptSession(it.value) }
            is SessionControllerResult.Failure -> preserveFailure(created)
        }
    }

    private fun recoverOnly(): SessionControllerResult<String> {
        // Never issue Snapshot/Observe/Stop/Destroy against an ID left behind
        // by a dead provider process. Recover is the authority for the
        // currently running Go manager.
        val recovered = SessionEnvelopeDecoder.decode(
            bridge.recover(commandID("recover")),
        ) { it.requiredSessionID() }
        return when (recovered) {
            is SessionControllerResult.Success -> recovered.also { adoptSession(it.value) }
            is SessionControllerResult.Failure -> {
                if (recovered.code == SessionFailureCode.NOT_FOUND) forgetSessionAfterNotFound()
                preserveFailure(recovered)
            }
        }
    }

    private fun commandID(operation: String): String {
        // ASCII-safe stable ID; the caller retains it for this logical command
        // while Swift reuses the same signed bytes across transport retries.
        return "ios-$operation-${Random.nextLong().toULong().toString(16)}"
    }

    private fun <T> preserveFailure(failure: SessionControllerResult.Failure): SessionControllerResult<T> =
        SessionControllerResult.Failure(failure.message, failure.code)

    private fun kotlinx.serialization.json.JsonObject.requiredSessionID(): String =
        requiredSessionIdentifier("session_id")
}
