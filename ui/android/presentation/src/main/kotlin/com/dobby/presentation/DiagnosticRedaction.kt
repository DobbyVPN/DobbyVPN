package com.dobby.presentation

/**
 * Redacts network identifiers from Android diagnostic record text.
 *
 * Mirrors core/log/redact.go in the Go backend: subscription URLs, hostnames
 * and IP addresses render as [redacted] while ports, protocols, levels and
 * error kinds stay readable. Keep the pattern set and order identical to the
 * Go implementation; the Go and Kotlin unit tables pin both sides.
 */
object DiagnosticRedaction {
    const val REDACTED = "[redacted]"

    private val patterns = listOf(
        Regex("""[A-Za-z][A-Za-z0-9+.\-]*://[^\s"\\]+"""),
        Regex("""\[[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*\]"""),
        Regex("""\b\d{1,3}(?:\.\d{1,3}){3}\b"""),
        Regex("""\b[0-9A-Fa-f]*(?::[0-9A-Fa-f]*){3,}\b"""),
        Regex("""\b(?:[A-Za-z0-9-]+\.)+[A-Za-z]{2,}\b"""),
    )

    fun redact(text: String): String =
        patterns.fold(text) { acc, pattern -> pattern.replace(acc, REDACTED) }
}
