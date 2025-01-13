/*
 * Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.
 * SPDX-License-Identifier: Apache-2.0
 */
package com.dobby.awg.config

import android.net.InetAddresses
import android.os.Build
import java.lang.reflect.Method
import java.net.InetAddress
import java.net.UnknownHostException
import java.util.regex.Pattern

/**
 * Utility methods for creating instances of [InetAddress].
 */
object InetAddresses {
    private val PARSER_METHOD: Method?
    private val WONT_TOUCH_RESOLVER: Pattern = Pattern.compile(
        "^(((([0-9A-Fa-f]{1,4}:){7}([0-9A-Fa-f]{1,4}|:))|(([0-9A-Fa-f]{1,4}:){6}(:[0-9A-Fa-f]{1,4}|((25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)(\\.(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)){3})|:))|(([0-9A-Fa-f]{1,4}:){5}(((:[0-9A-Fa-f]{1,4}){1,2})|:((25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)(\\.(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)){3})|:))|(([0-9A-Fa-f]{1,4}:){4}(((:[0-9A-Fa-f]{1,4}){1,3})|((:[0-9A-Fa-f]{1,4})?:((25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)(\\.(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)){3}))|:))|(([0-9A-Fa-f]{1,4}:){3}(((:[0-9A-Fa-f]{1,4}){1,4})|((:[0-9A-Fa-f]{1,4}){0,2}:((25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)(\\.(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)){3}))|:))|(([0-9A-Fa-f]{1,4}:){2}(((:[0-9A-Fa-f]{1,4}){1,5})|((:[0-9A-Fa-f]{1,4}){0,3}:((25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)(\\.(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)){3}))|:))|(([0-9A-Fa-f]{1,4}:){1}(((:[0-9A-Fa-f]{1,4}){1,6})|((:[0-9A-Fa-f]{1,4}){0,4}:((25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)(\\.(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)){3}))|:))|(:(((:[0-9A-Fa-f]{1,4}){1,7})|((:[0-9A-Fa-f]{1,4}){0,5}:((25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)(\\.(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)){3}))|:)))(%.+)?)|((?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?))$"
    )
    private val VALID_HOSTNAME: Pattern =
        Pattern.compile("^(?=.{1,255}$)[0-9A-Za-z](?:(?:[0-9A-Za-z]|-){0,61}[0-9A-Za-z])?(?:\\.[0-9A-Za-z](?:(?:[0-9A-Za-z]|-){0,61}[0-9A-Za-z])?)*\\.?$")

    init {
        var m: Method? = null
        try {
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) // noinspection JavaReflectionMemberAccess
                m = InetAddress::class.java.getMethod("parseNumericAddress", String::class.java)
        } catch (ignored: Exception) {
        }
        PARSER_METHOD = m
    }

    /**
     * Determines whether input is a valid DNS hostname.
     *
     * @param maybeHostname a string that is possibly a DNS hostname
     * @return whether or not maybeHostname is a valid DNS hostname
     */
    fun isHostname(maybeHostname: CharSequence?): Boolean {
        return VALID_HOSTNAME.matcher(maybeHostname).matches()
    }

    /**
     * Parses a numeric IPv4 or IPv6 address without performing any DNS lookups.
     *
     * @param address a string representing the IP address
     * @return an instance of [Inet4Address] or [Inet6Address], as appropriate
     */
    @Throws(ParseException::class)
    fun parse(address: String): InetAddress {
        if (address.isEmpty()) throw ParseException(
            InetAddress::class.java,
            address,
            "Empty address"
        )
        return try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) InetAddresses.parseNumericAddress(
                address
            )
            else if (PARSER_METHOD != null) PARSER_METHOD.invoke(null, address) as InetAddress
            else throw NoSuchMethodException("parseNumericAddress")
        } catch (e: IllegalArgumentException) {
            throw ParseException(InetAddress::class.java, address, e)
        } catch (e: Exception) {
            val cause = e.cause
            // Re-throw parsing exceptions with the original type, as callers might try to catch
            // them. On the other hand, callers cannot be expected to handle reflection failures.
            if (cause is IllegalArgumentException) throw ParseException(
                InetAddress::class.java, address, cause
            )
            try {
                if (WONT_TOUCH_RESOLVER.matcher(address).matches()) InetAddress.getByName(address)
                else throw ParseException(InetAddress::class.java, address, "Not an IP address")
            } catch (f: UnknownHostException) {
                throw ParseException(InetAddress::class.java, address, f)
            }
        }
    }
}
