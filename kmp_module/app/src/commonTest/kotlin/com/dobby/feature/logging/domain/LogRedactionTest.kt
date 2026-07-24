package com.dobby.feature.logging.domain

import kotlin.test.Test
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
    fun redacts_network_and_control_identifiers() {
        val redacted = redactLog(
            "server=198.51.100.42 session_id=internal-command-id session_state=connected",
        )

        assertFalse(redacted.contains("198.51.100.42"))
        assertFalse(redacted.contains("internal-command-id"))
        assertTrue(redacted.contains("server=[REDACTED]"))
        assertTrue(redacted.contains("session_id=[REDACTED]"))
        assertTrue(redacted.contains("session_state=connected"))
    }
}
