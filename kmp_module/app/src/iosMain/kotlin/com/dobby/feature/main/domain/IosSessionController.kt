package com.dobby.feature.main.domain

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

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
        SessionEnvelopeDecoder.decode(bridge.configure(rawConfig)) { it.toSessionConfiguration() }
    }

    override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> = mutex.withLock {
        val mode = if (target is SessionStartTarget.AutoSelect) "AUTO_SELECT" else "PROFILE_INDEX"
        val index = (target as? SessionStartTarget.ProfileIndex)?.index ?: 0
        SessionEnvelopeDecoder.decode(bridge.start(mode, index)) { it.sessionLong("generation").toULong() }
    }

    override suspend fun stop(generation: ULong): SessionControllerResult<ULong> = mutex.withLock {
        SessionEnvelopeDecoder.decode(bridge.stop(generation.toLong())) { it.sessionLong("generation").toULong() }
    }

    override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> = mutex.withLock {
        SessionEnvelopeDecoder.decode(bridge.snapshot()) { it.toSessionSnapshot() }
    }

    override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> = mutex.withLock {
        SessionEnvelopeDecoder.decode(bridge.observe(afterSequence.toLong())) { it.toSessionObservation() }
    }

    override suspend fun destroy(): SessionControllerResult<Unit> = mutex.withLock {
        SessionEnvelopeDecoder.decode(bridge.destroy()) { Unit }
    }
}
