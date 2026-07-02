package com.dobby.feature.main.domain.config

import kotlinx.cinterop.ByteVar
import kotlinx.cinterop.CPointerVar
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.alloc
import kotlinx.cinterop.allocArray
import kotlinx.cinterop.convert
import kotlinx.cinterop.memScoped
import kotlinx.cinterop.pointed
import kotlinx.cinterop.ptr
import kotlinx.cinterop.toKString
import kotlinx.cinterop.value
import platform.posix.AF_INET
import platform.posix.NI_MAXHOST
import platform.posix.NI_NUMERICHOST
import platform.posix.addrinfo
import platform.posix.freeaddrinfo
import platform.posix.getaddrinfo
import platform.posix.getnameinfo

internal actual object ProfileDnsResolver {
    @OptIn(ExperimentalForeignApi::class)
    actual fun resolveIpv4(host: String): String? {
        if (host.isIpv4Literal()) return host

        return memScoped {
            val hints = alloc<addrinfo> {
                ai_flags = 0
                ai_family = AF_INET
                ai_socktype = 0
                ai_protocol = 0
                ai_addrlen = 0.convert()
                ai_addr = null
                ai_canonname = null
                ai_next = null
            }
            val result = alloc<CPointerVar<addrinfo>>()
            val rc = getaddrinfo(host, null, hints.ptr, result.ptr)
            if (rc != 0) return@memScoped null

            try {
                var current = result.value
                while (current != null) {
                    val buffer = allocArray<ByteVar>(NI_MAXHOST)
                    val nameRc = getnameinfo(
                        current.pointed.ai_addr,
                        current.pointed.ai_addrlen,
                        buffer,
                        NI_MAXHOST.convert(),
                        null,
                        0.convert(),
                        NI_NUMERICHOST
                    )
                    if (nameRc == 0) {
                        val ip = buffer.toKString()
                        if (ip.isIpv4Literal()) return@memScoped ip
                    }
                    current = current.pointed.ai_next
                }
                null
            } finally {
                freeaddrinfo(result.value)
            }
        }
    }
}
