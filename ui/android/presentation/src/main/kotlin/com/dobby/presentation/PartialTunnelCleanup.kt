package com.dobby.presentation

/** Tracks the independently retryable phases for one tunnel generation. */
class PartialTunnelCleanup(
    private val hasDescriptor: () -> Boolean,
    private val closeDescriptor: () -> Unit,
    private val hasNetwork: () -> Boolean,
    private val cancelDns: () -> Unit,
    private val stopNetwork: () -> Unit,
) {
    private var dnsCanceled = false

    fun release(): Throwable? {
        var failure: Throwable? = null
        fun attempt(block: () -> Unit) {
            try { block() } catch (error: Throwable) {
                val previous = failure
                if (previous == null) failure = error else previous.addSuppressed(error)
            }
        }
        if (hasNetwork() && !dnsCanceled) attempt { cancelDns(); dnsCanceled = true }
        if (hasDescriptor()) attempt { closeDescriptor() }
        // Network ownership remains necessary while the TUN is still live.
        if (!hasDescriptor() && hasNetwork() && dnsCanceled) attempt { stopNetwork() }
        return failure
    }
}
