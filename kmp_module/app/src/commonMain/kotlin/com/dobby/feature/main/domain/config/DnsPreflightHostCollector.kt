package com.dobby.feature.main.domain.config

import com.dobby.feature.main.domain.ConnectionProfile
import com.dobby.feature.main.domain.OutlineConfig
import com.dobby.feature.main.domain.TrustTunnelConfig
import com.dobby.feature.main.domain.VpnInterface
import com.dobby.feature.main.domain.XrayClientConfig
import net.peanuuutz.tomlkt.Toml
import net.peanuuutz.tomlkt.TomlArray
import net.peanuuutz.tomlkt.TomlElement
import net.peanuuutz.tomlkt.TomlLiteral
import net.peanuuutz.tomlkt.TomlTable
import net.peanuuutz.tomlkt.decodeFromString

internal fun collectDnsPreflightHosts(profiles: List<ConnectionProfile>): List<String> {
    val hosts = linkedSetOf<String>()
    profiles.forEach { profile ->
        when (profile.protocol) {
            VpnInterface.CLOAK_OUTLINE -> hosts += collectOutlineHosts(profile.payload)
            VpnInterface.XRAY -> hosts += collectXrayHosts(profile.payload)
            VpnInterface.TRUST_TUNNEL -> hosts += collectTrustTunnelHosts(profile.payload)
            VpnInterface.NONE -> Unit
        }
    }
    return hosts.mapNotNull(::normalizeHost).distinct()
}

internal fun isLocalOrIpLiteral(host: String): Boolean {
    if (host == "localhost" || host == "127.0.0.1" || host == "::1") return true
    if (ipv4Literal.matches(host)) return true
    return host.contains(":")
}

private fun collectOutlineHosts(payload: String): List<String> {
    val config = runCatching { Toml.decodeFromString<OutlineConfig>(payload) }.getOrNull() ?: return emptyList()
    val host = if (config.Cloak == true) {
        config.RemoteHost ?: config.Server
    } else {
        config.Server
    }
    return listOfNotNull(host)
}

private fun collectTrustTunnelHosts(payload: String): List<String> {
    val config = runCatching { Toml.decodeFromString<TrustTunnelConfig>(payload) }.getOrNull() ?: return emptyList()
    return listOfNotNull(config.endpoint?.hostname, config.endpoint?.custom_sni) + config.endpoint?.addresses.orEmpty()
}

private fun collectXrayHosts(payload: String): List<String> {
    val config = runCatching { Toml.decodeFromString<XrayClientConfig>(payload) }.getOrNull() ?: return emptyList()
    val outbounds = config.outbounds as? TomlArray ?: return emptyList()
    return outbounds.flatMap { outbound ->
        val table = outbound as? TomlTable ?: return@flatMap emptyList()
        val protocol = table.literal("protocol") ?: return@flatMap emptyList()
        if (protocol in setOf("freedom", "blackhole", "dns")) return@flatMap emptyList()
        val settings = table["settings"] as? TomlTable ?: return@flatMap emptyList()

        when (protocol) {
            "vless", "vmess", "vlite" -> collectAddressArray(settings["vnext"])
            "trojan", "shadowsocks", "socks", "http", "mtproto" -> collectAddressArray(settings["servers"])
            "wireguard" -> collectEndpointArray(settings["peers"])
            else -> emptyList()
        }
    }
}

private fun collectAddressArray(element: TomlElement?): List<String> {
    val array = element as? TomlArray ?: return emptyList()
    return array.mapNotNull { (it as? TomlTable)?.literal("address") }
}

private fun collectEndpointArray(element: TomlElement?): List<String> {
    val array = element as? TomlArray ?: return emptyList()
    return array.mapNotNull { peer ->
        val endpoint = (peer as? TomlTable)?.literal("endpoint")
        endpoint?.let(::extractEndpointHost)
    }
}

private fun TomlTable.literal(key: String): String? = (this[key] as? TomlLiteral)?.content

private fun extractEndpointHost(endpoint: String): String? {
    val trimmed = endpoint.trim()
    if (trimmed.isEmpty()) return null

    val authority = trimmed.substringAfter("://", trimmed).substringBefore('/').substringBefore('?')
    if (authority.startsWith("[")) return authority.substringAfter('[').substringBefore(']')
    if (authority.count { it == ':' } == 1) return authority.substringBefore(':')
    return authority
}

private fun normalizeHost(host: String): String? {
    val normalized = extractEndpointHost(host)
        ?.trim()
        ?.trimEnd('.')
        ?.lowercase()
        .orEmpty()
    return normalized.ifEmpty { null }
}

private val ipv4Literal = Regex("""^\d{1,3}(\.\d{1,3}){3}$""")
