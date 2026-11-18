package com.dobby.presentation

/** The first error the user has not closed yet. */
fun shownError(errors: List<String>, dismissed: Set<String>): String? = errors.firstOrNull { it !in dismissed }

/** Closed errors are forgotten once they disappear from the current errors, so a later repeat shows again. */
fun forgetClearedDismissals(dismissed: Set<String>, errors: List<String>): Set<String> = dismissed.intersect(errors.toSet())
