package com.dobby.presentation

/** The first error the user has not closed yet. */
fun shownError(errors: List<String>, dismissed: Set<String>): String? = errors.firstOrNull { it !in dismissed }

/** Closed errors are forgotten once they disappear from the current errors, so a later repeat shows again. */
fun forgetClearedDismissals(dismissed: Set<String>, errors: List<String>): Set<String> = dismissed.intersect(errors.toSet())

/** The session failure is shown only in FAILED, IDLE or CONFIGURED, and never while a new attempt is starting. */
fun sessionFailureVisible(state: String, attemptStarting: Boolean): Boolean =
    !attemptStarting && (state == "FAILED" || state == "IDLE" || state == "CONFIGURED")
