package com.dobby.feature.main.domain.config

import java.net.Inet4Address
import java.net.InetAddress

internal actual object ProfileDnsResolver {
    actual fun resolveIpv4(host: String): String? {
        if (host.isIpv4Literal()) return host
        return InetAddress.getAllByName(host)
            .firstOrNull { it is Inet4Address }
            ?.hostAddress
            ?.takeIf { it.isIpv4Literal() }
    }
}
