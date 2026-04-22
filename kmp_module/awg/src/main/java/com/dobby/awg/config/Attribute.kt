/*
 * Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 */
package com.dobby.awg.config

import java.util.Optional
import java.util.regex.Pattern

class Attribute private constructor(val key: String, val value: String) {
    companion object {
        private val LINE_PATTERN: Pattern = Pattern.compile("(\\w+)\\s*=\\s*([^\\s#][^#]*)")
        private val LIST_SEPARATOR: Pattern = Pattern.compile("\\s*,\\s*")

        fun join(values: Iterable<*>): String {
            val it = values.iterator()
            if (!it.hasNext()) {
                return ""
            }
            val sb = StringBuilder()
            sb.append(it.next())
            while (it.hasNext()) {
                sb.append(", ")
                sb.append(it.next())
            }
            return sb.toString()
        }

        fun parse(line: CharSequence?): Optional<Attribute> {
            val matcher = LINE_PATTERN.matcher(line)
            if (!matcher.matches()) return Optional.empty()
            return Optional.of(Attribute(matcher.group(1), matcher.group(2)))
        }

        fun split(value: CharSequence?): Array<String> {
            return LIST_SEPARATOR.split(value)
        }
    }
}
