package com.dobby.presentation

/** Small, injectable boundary for timing UI operations without making diagnostics part of their result. */
class UiOperationDiagnostics(
    private val clockNs: () -> Long,
    private val emit: (Observation) -> Unit,
    private val beginTrace: (String) -> Unit = {},
    private val endTrace: () -> Unit = {},
    private val reportFailure: (String, Throwable, Throwable?) -> Unit = { _, _, _ -> },
) {
    data class Observation(
        val operation: String,
        val phase: String,
        val startedNs: Long,
        val finishedNs: Long? = null,
        val failure: Throwable? = null,
    )

    fun <T> observe(operation: String, operationBlock: () -> T): T {
        var primaryFailure: Throwable? = null
        var firstDiagnosticFailure: Throwable? = null

        fun addSuppressed(target: Throwable, secondary: Throwable) {
            if (target === secondary) return
            try {
                target.addSuppressed(secondary)
            } catch (_: Throwable) {
                // A diagnostic failure must not replace the operation's result or exception.
            }
        }

        fun report(context: String, failure: Throwable) {
            val firstDiagnostic = firstDiagnosticFailure
            if (firstDiagnostic == null) firstDiagnosticFailure = failure else addSuppressed(firstDiagnostic, failure)
            val primary = primaryFailure
            if (primary != null) addSuppressed(primary, failure)
            try {
                reportFailure(context, failure, primary)
            } catch (reporterFailure: Throwable) {
                val firstDiagnostic = firstDiagnosticFailure
                if (firstDiagnostic == null) firstDiagnosticFailure = reporterFailure else addSuppressed(firstDiagnostic, reporterFailure)
                addSuppressed(failure, reporterFailure)
                if (primary != null) addSuppressed(primary, reporterFailure)
                try {
                    System.err.println("UI diagnostic reporter failed during $context: ${reporterFailure.stackTraceToString()}")
                } catch (stderrFailure: Throwable) {
                    addSuppressed(failure, stderrFailure)
                    if (primary != null) addSuppressed(primary, stderrFailure)
                }
            }
        }

        fun readClock(context: String): Long? = try {
            clockNs()
        } catch (failure: Throwable) {
            report("$operation clock.$context", failure)
            null
        }

        fun emitSafely(observation: Observation) {
            try {
                emit(observation)
            } catch (failure: Throwable) {
                report("$operation emit.${observation.phase}", failure)
            }
        }

        val startedNs = readClock("start") ?: -1L
        emitSafely(Observation(operation, "entry", startedNs))

        var traceStarted = false
        try {
            beginTrace(operation)
            traceStarted = true
        } catch (failure: Throwable) {
            report("$operation trace.begin", failure)
        }

        try {
            return operationBlock()
        } catch (failure: Throwable) {
            primaryFailure = failure
            firstDiagnosticFailure?.let { addSuppressed(failure, it) }
            throw failure
        } finally {
            val finishedNs = readClock("finish")
            if (traceStarted) {
                try {
                    endTrace()
                } catch (failure: Throwable) {
                    report("$operation trace.end", failure)
                }
            }
            val failure = primaryFailure
            emitSafely(
                Observation(
                    operation = operation,
                    phase = if (failure == null) "complete" else "error",
                    startedNs = startedNs,
                    finishedNs = finishedNs,
                    failure = failure,
                ),
            )
        }
    }
}
