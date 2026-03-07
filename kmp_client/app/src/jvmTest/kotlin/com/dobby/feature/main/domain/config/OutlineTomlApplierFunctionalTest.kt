package com.dobby.feature.main.domain.config

import com.dobby.feature.main.domain.OutlineConfig
import com.dobby.test.fixtures.createTestLogger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class OutlineTomlApplierFunctionalTest {

    @Test
    fun `websocket true without port defaults to 443`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = null, // no port
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            WebSocket = true // should default port to 443
        )

        val result = applier.apply(outline)

        assertTrue(outlineRepo.isOutlineEnabled)
        assertEquals("example.org:443", outlineRepo.serverPort)
        assertTrue(result != null)
    }

    @Test
    fun `websocket false without port returns null and disables outline`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = null, // no port
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            WebSocket = false // no default port
        )

        val result = applier.apply(outline)

        assertNull(result)
        assertFalse(outlineRepo.isOutlineEnabled)
    }

    @Test
    fun `websocket path generates tcp and udp paths`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            WebSocket = true,
            WebSocketPath = "/ws"
        )

        applier.apply(outline)

        assertTrue(outlineRepo.websocketEnabled)
        assertEquals("/ws/tcp", outlineRepo.tcpPath)
        assertEquals("/ws/udp", outlineRepo.udpPath)
    }

    @Test
    fun `websocket path with trailing slash is trimmed`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            WebSocket = true,
            WebSocketPath = "/path/to/ws/"
        )

        applier.apply(outline)

        assertEquals("/path/to/ws/tcp", outlineRepo.tcpPath)
        assertEquals("/path/to/ws/udp", outlineRepo.udpPath)
    }

    @Test
    fun `websocket enabled without path sets empty paths`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            WebSocket = true,
            WebSocketPath = null // no path
        )

        applier.apply(outline)

        assertTrue(outlineRepo.websocketEnabled)
        assertEquals("", outlineRepo.tcpPath)
        assertEquals("", outlineRepo.udpPath)
    }

    @Test
    fun `websocket disabled clears paths`() {
        val outlineRepo = FakeOutlineRepo(tcpPath = "old/tcp", udpPath = "old/udp")
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            WebSocket = false,
            WebSocketPath = "/ignored"
        )

        applier.apply(outline)

        assertFalse(outlineRepo.websocketEnabled)
        assertEquals("", outlineRepo.tcpPath)
        assertEquals("", outlineRepo.udpPath)
    }

    @Test
    fun `disguise prefix with spaces is preserved`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            DisguisePrefix = "POST " // trailing space is intentional
        )

        applier.apply(outline)

        assertEquals("POST ", outlineRepo.prefix)
    }

    @Test
    fun `disguise prefix null sets empty string`() {
        val outlineRepo = FakeOutlineRepo(prefix = "old-prefix")
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            DisguisePrefix = null
        )

        applier.apply(outline)

        assertEquals("", outlineRepo.prefix)
    }

    @Test
    fun `cloak enabled redirects to localhost 1984`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "ignored-server.org",
            Port = 9999,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            Cloak = true
        )

        val result = applier.apply(outline)

        assertTrue(outlineRepo.isOutlineEnabled)
        assertEquals("127.0.0.1:1984", outlineRepo.serverPort)
        assertEquals(1984, cloakRepo.cloakLocalPortValue)
        assertTrue(result?.first == true) // cloakEnabled
    }

    @Test
    fun `cloak disabled uses server and port directly`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "direct-server.org",
            Port = 8443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            Cloak = false
        )

        val result = applier.apply(outline)

        assertTrue(outlineRepo.isOutlineEnabled)
        assertEquals("direct-server.org:8443", outlineRepo.serverPort)
        assertTrue(result?.first == false) // cloakEnabled = false
    }

    @Test
    fun `method defaults to chacha20-ietf-poly1305 when not provided`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = null, // should default
            Password = "secret"
        )

        applier.apply(outline)

        assertEquals("chacha20-ietf-poly1305:secret", outlineRepo.methodPassword)
    }

    @Test
    fun `missing password returns null and clears configs`() {
        val outlineRepo = FakeOutlineRepo(
            isOutlineEnabled = true,
            methodPassword = "old:pass",
            serverPort = "old:1"
        )
        val cloakRepo = FakeCloakRepo(initialCloakEnabled = true)
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = null // missing
        )

        val result = applier.apply(outline)

        assertNull(result)
        assertFalse(outlineRepo.isOutlineEnabled)
        assertEquals("", outlineRepo.methodPassword)
        assertFalse(cloakRepo.cloakEnabledValue)
    }

    @Test
    fun `missing server without cloak returns null and clears configs`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = null, // missing
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            Cloak = false
        )

        val result = applier.apply(outline)

        assertNull(result)
        assertFalse(outlineRepo.isOutlineEnabled)
    }

    @Test
    fun `whitespace-only password is treated as missing`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "   " // whitespace only
        )

        val result = applier.apply(outline)

        assertNull(result)
        assertFalse(outlineRepo.isOutlineEnabled)
    }

    @Test
    fun `cloak disabled clears cloak config`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo(
            initialCloakEnabled = true,
            initialCloakConfig = "old-json"
        )
        val applier = OutlineTomlApplier(outlineRepo, cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Method = "chacha20-ietf-poly1305",
            Password = "secret",
            Cloak = false
        )

        applier.apply(outline)

        assertFalse(cloakRepo.cloakEnabledValue)
        assertEquals("", cloakRepo.cloakConfigValue)
    }
}
