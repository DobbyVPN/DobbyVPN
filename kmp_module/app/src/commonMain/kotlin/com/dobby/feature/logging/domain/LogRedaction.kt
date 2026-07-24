package com.dobby.feature.logging.domain

private val urlPattern = Regex("(?i)\\b(?:https?|ss|vless|vmess|trojan)://[^\\s\\\"']+")
private val sensitiveKeys = listOf(
    "token", "api[_-]?key", "password", "secret", "credential", "authorization",
    "auth", "endpoint", "url", "config", "server", "host", "address", "session[_-]?id", "command[_-]?id",
).joinToString("|")
private val secretPattern = Regex(
    """(?i)(["']?(?:$sensitiveKeys)["']?\s*[:=]\s*)""" +
        """(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|[^\s,}\]]+)"""
)
private val tomlPattern = Regex("""(?m)^\s*\[{1,2}[A-Za-z0-9_.-]+\]{1,2}\s*$""")
private val jsonConfigPattern = Regex("""["'][A-Za-z0-9_.-]+["']\s*:""")
private val ipv4Pattern = Regex("""\b(?:\d{1,3}\.){3}\d{1,3}\b""")

/** Central local-log redaction for values which may originate from configuration or metadata. */
fun redactLog(message: String): String {
    if (tomlPattern.containsMatchIn(message) || (message.contains("{") && jsonConfigPattern.containsMatchIn(message))) {
        return "[REDACTED CONFIGURATION]"
    }
    val keyedValuesRedacted = secretPattern.replace(urlPattern.replace(message, "[REDACTED URL]")) { match ->
        "${match.groupValues[1]}[REDACTED]"
    }
    return ipv4Pattern.replace(keyedValuesRedacted, "[REDACTED IP]")
}
