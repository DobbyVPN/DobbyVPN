package com.dobby.feature.logging.domain

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LogEventTest {
    @Test
    fun encodes_complete_machine_json_and_renders_one_human_readable_line() {
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
        assertTrue(encoded.contains("vpn.example.invalid"))
        assertFalse(encoded.contains('\n'))
        assertEquals(
            "[2026-07-29 12:34:56.789] [DEBUG] [kmp] Connected generation=4 endpoint=vpn.example.invalid:443 · endpoint=vpn.example.invalid:443 · state=CONNECTED",
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
