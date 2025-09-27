package com.dobby.feature.main.domain

enum class Protocols(val protocolName: String) {
    SHADOWSOCKS("shadowsocks"),
    SHADOWSOCKS_VIA_CLOAK("shadowsocks via cloak");

    companion object {
        fun fromString(value: String): Protocols? {
            return values().firstOrNull { value.lowercase().contains(it.protocolName) }
        }
    }
}
