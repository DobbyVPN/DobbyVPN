package com.dobby.feature.logging.domain

private val urlPattern = Regex("(?i)\\b(?:https?|ss|vless|vmess|trojan)://[^\\s\\\"']+")
private val sensitiveKeys = listOf(
    "token", "api[_-]?key", "password", "secret", "credential", "authorization",
    "auth", "endpoint", "url", "config", "server(?:ip|_ip)?", "host", "address",
    "remote", "proxy", "gateway", "dest(?:ination)?", "resolved", "path", "file",
    "directory", "session[_-]?id", "command[_-]?id",
).joinToString("|")
private val sensitiveFieldKeyPattern = Regex("(?i)($sensitiveKeys)")
private val secretPattern = Regex(
    """(?i)(["']?(?:$sensitiveKeys)["']?\s*[:=]\s*)""" +
        """(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|[^\s,}\]]+)""",
)
private val tomlPattern = Regex("""(?m)^\s*\[{1,2}[A-Za-z0-9_.-]+\]{1,2}\s*$""")
private val jsonConfigPattern = Regex("""["'][A-Za-z0-9_.-]+["']\s*:""")
private val networkEndpointPattern = Regex(
    """(?i)(?:[^\s:@]+:[^\s@]+@)?(?:\[[0-9a-f:.]+\](?::\d{1,5})?|""" +
        """\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b|""" +
        """\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z]{2,63}|local|invalid):\d{1,5}\b)""",
)
private val ipv6Pattern = Regex("""(?i)\b[0-9a-f]{1,4}(?::[0-9a-f]{0,4}){2,7}\b""")
private val hostnamePattern = Regex(
    """(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z]{2,63}|local|invalid)\b""",
)
private val sourceSuffixes = listOf(".go", ".swift", ".kt", ".kts", ".proto", ".json", ".toml", ".yaml", ".yml", ".xml", ".md")

/** Central local-log redaction for values which may originate from configuration or metadata. */
fun redactLog(message: String): String {
    if (tomlPattern.containsMatchIn(message) || (message.contains("{") && jsonConfigPattern.containsMatchIn(message))) {
        return "[REDACTED CONFIGURATION]"
    }
    var redacted = urlPattern.replace(message, "[REDACTED URL]")
    redacted = secretPattern.replace(redacted) { match -> "${match.groupValues[1]}[REDACTED]" }
    redacted = networkEndpointPattern.replace(redacted, "[REDACTED ENDPOINT]")
    redacted = ipv6Pattern.replace(redacted) { match ->
        val candidate = match.value
        if (candidate.contains("::") || candidate.any { it.lowercaseChar() in 'a'..'f' } || candidate.count { it == ':' } >= 3) {
            "[REDACTED ENDPOINT]"
        } else {
            candidate
        }
    }
    return hostnamePattern.replace(redacted) { match ->
        if (sourceSuffixes.any { match.value.endsWith(it, ignoreCase = true) }) match.value else "[REDACTED ENDPOINT]"
    }
}

internal fun redactLogField(key: String, value: String): String =
    if (sensitiveFieldKeyPattern.containsMatchIn(key)) "[REDACTED]" else redactLog(value)
