package com.dobby.feature.logging.domain

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LogRedactionTest {
    @Test
    fun redacts_urls_credentials_and_configuration() {
        val redacted = redactLog("endpoint=https://user:secret@example.invalid token=secret-value")

        assertFalse(redacted.contains("user:secret"))
        assertFalse(redacted.contains("secret-value"))
        assertTrue(redacted.contains("[REDACTED"))
        assertTrue(redactLog("[[Outline]]\nPassword='secret'").contains("REDACTED CONFIGURATION"))
        assertTrue(redactLog("[Outline]\nPassword='secret'").contains("REDACTED CONFIGURATION"))
        assertTrue(redactLog("{\n  \"token\": \"secret\"\n}").contains("REDACTED CONFIGURATION"))
        assertTrue(redactLog("config={\"UID\":\"secret\"}").contains("REDACTED CONFIGURATION"))
    }

    @Test
    fun redacts_network_control_and_path_identifiers_but_keeps_source_names() {
        val redacted = redactLog(
            "server=198.51.100.42 remote=vpn.example.invalid:443 ipv6=fd00:dbb::1 " +
                "lookup=vpn.example.invalid session_id=internal-command-id path=/Users/person/private.log " +
                "session_state=connected source=Logger.swift",
        )

        listOf(
            "198.51.100.42",
            "vpn.example.invalid",
            "fd00:dbb::1",
            "internal-command-id",
            "/Users/person/private.log",
        ).forEach { assertFalse(redacted.contains(it), redacted) }
        assertTrue(redacted.contains("session_state=connected"))
        assertTrue(redacted.contains("source=Logger.swift"))
    }

    @Test
    fun redacts_compound_sensitive_field_names() {
        listOf(
            "sessionToken",
            "proxyUrl",
            "serverHost",
            "api_key_hash",
            "command-id",
            "configurationPath",
        ).forEach { key ->
            assertEquals("[REDACTED]", redactLogField(key, "opaque-secret"), key)
        }
        assertEquals("CONNECTED", redactLogField("sessionState", "CONNECTED"))
        assertEquals("Logger.swift", redactLogField("source", "Logger.swift"))
    }

    @Test
    fun encodes_machine_json_and_renders_one_human_readable_line() {
        val encoded = encodeLogEvent(
            timestamp = "2026-07-29T12:34:56.789Z",
            level = LogLevel.DEBUG,
            source = "kmp",
            event = "session.status",
            message = "Connected generation=4 endpoint=vpn.example.invalid:443",
            fields = mapOf("state" to "CONNECTED", "endpoint" to "vpn.example.invalid:443"),
        )
        val event = Json.parseToJsonElement(encoded).jsonObject

        assertEquals("dobby.log/v1", event["schema"]?.jsonPrimitive?.content)
        assertEquals("DEBUG", event["level"]?.jsonPrimitive?.content)
        assertEquals("session.status", event["event"]?.jsonPrimitive?.content)
        assertFalse(encoded.contains("vpn.example.invalid"))
        assertFalse(encoded.contains('\n'))
        assertEquals(
            "[2026-07-29 12:34:56.789] [DEBUG] [kmp] Connected generation=4 endpoint=[REDACTED] · endpoint=[REDACTED] · state=CONNECTED",
            renderLogLine(encoded),
        )
    }

    @Test
    fun preserves_legacy_failure_and_warning_severity() {
        assertEquals(LogLevel.ERROR, LogLevel.fromLegacyMessage("setTunnelNetworkSettings failed during apply"))
        assertEquals(LogLevel.ERROR, LogLevel.fromLegacyMessage("Error starting packet tunnel"))
        assertEquals(LogLevel.WARN, LogLevel.fromLegacyMessage("Retry with compatibility transport"))
        assertEquals(LogLevel.INFO, LogLevel.fromLegacyMessage("Tunnel connected and healthy"))
    }

    @Test
    fun preserves_subsecond_timestamp_for_cross_producer_ordering() {
        val encoded = encodeLogEvent(
            timestamp = "2026-07-29T12:34:56.123456789Z",
            level = LogLevel.INFO,
            source = "go",
            event = "status.snapshot",
            message = "status",
        )
        assertEquals("2026-07-29 12:34:56.123456789Z", comparableLogTimestamp(encoded))
        assertTrue(renderLogLine(encoded).contains("[go]"))
    }

    @Test
    fun preserves_legacy_human_lines_during_migration() {
        val legacy = "[2026-07-29 12:34:56] [DEBUG] existing diagnostic"
        assertEquals(legacy, renderLogLine(legacy))
        assertEquals("2026-07-29 12:34:56", comparableLogTimestamp(legacy))
    }
}
