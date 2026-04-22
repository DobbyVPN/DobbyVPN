/*
 * Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 */
package com.dobby.awg.config

/**
 *
 */
class ParseException @JvmOverloads constructor(
    val parsingClass: Class<*>, val text: CharSequence?,
    message: String? = null, cause: Throwable? = null
) : Exception(message, cause) {
    constructor(
        parsingClass: Class<*>, text: CharSequence?,
        cause: Throwable?
    ) : this(parsingClass, text, null, cause)
}
