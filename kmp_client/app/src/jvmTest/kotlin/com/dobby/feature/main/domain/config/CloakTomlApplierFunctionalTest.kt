package com.dobby.feature.main.domain.config

import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.OutlineConfig
import com.dobby.test.fixtures.createTestLogger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class CloakTomlApplierFunctionalTest {

    @Test
    fun `full valid cloak config generates correct JSON and enables cloak`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid-12345",
            PublicKey = "test-public-key",
            ServerName = "cdn.example.org",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            CDNWsUrlPath = "/ws/path",
            CDNOriginHost = "origin.example.org",
            NumConn = 4,
            BrowserSig = "chrome",
            StreamTimeout = 600,
            ProxyMethod = "shadowsocks"
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        val json = cloakRepo.cloakConfigValue
        assertTrue(json.contains("\"Transport\": \"CDN\""))
        assertTrue(json.contains("\"EncryptionMethod\": \"aes-gcm\""))
        assertTrue(json.contains("\"UID\": \"test-uid-12345\""))
        assertTrue(json.contains("\"PublicKey\": \"test-public-key\""))
        assertTrue(json.contains("\"ServerName\": \"cdn.example.org\""))
        assertTrue(json.contains("\"RemoteHost\": \"remote.example.org\""))
        assertTrue(json.contains("\"RemotePort\": \"8443\""))
        assertTrue(json.contains("\"CDNWsUrlPath\": \"/ws/path\""))
        assertTrue(json.contains("\"CDNOriginHost\": \"origin.example.org\""))
        assertTrue(json.contains("\"NumConn\": 4"))
        assertTrue(json.contains("\"BrowserSig\": \"chrome\""))
        assertTrue(json.contains("\"StreamTimeout\": 600"))
        assertTrue(json.contains("\"ProxyMethod\": \"shadowsocks\""))
    }

    @Test
    fun `cloak disabled clears config`() {
        val cloakRepo = FakeCloakRepo(
            initialCloakEnabled = true,
            initialCloakConfig = "old-json"
        )
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        applier.apply(OutlineConfig(), cloakEnabled = false)

        assertFalse(cloakRepo.cloakEnabledValue)
        assertEquals("", cloakRepo.cloakConfigValue)
    }

    @Test
    fun `missing required UID disables cloak`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = null, // missing
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443"
        )

        applier.apply(outline, cloakEnabled = true)

        assertFalse(cloakRepo.cloakEnabledValue)
        assertEquals("", cloakRepo.cloakConfigValue)
    }

    @Test
    fun `missing required PublicKey disables cloak`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = null, // missing
            RemoteHost = "remote.example.org",
            RemotePort = "8443"
        )

        applier.apply(outline, cloakEnabled = true)

        assertFalse(cloakRepo.cloakEnabledValue)
        assertEquals("", cloakRepo.cloakConfigValue)
    }

    @Test
    fun `missing required EncryptionMethod disables cloak`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = null, // missing
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443"
        )

        applier.apply(outline, cloakEnabled = true)

        assertFalse(cloakRepo.cloakEnabledValue)
        assertEquals("", cloakRepo.cloakConfigValue)
    }

    @Test
    fun `RemoteHost falls back to Server when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "fallback-server.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = null, // should fallback to Server
            RemotePort = "8443"
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"RemoteHost\": \"fallback-server.org\""))
    }

    @Test
    fun `RemotePort falls back to Port when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 9999,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = null // should fallback to Port
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"RemotePort\": \"9999\""))
    }

    @Test
    fun `RemotePort falls back to 443 when Port also not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = null, // no Port
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = null // should fallback to default 443
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"RemotePort\": \"443\""))
    }

    @Test
    fun `ServerName falls back to Server when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "main-server.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            ServerName = null // should fallback to Server
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"ServerName\": \"main-server.org\""))
    }

    @Test
    fun `CDNOriginHost falls back to Server when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "origin-fallback.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            CDNOriginHost = null // should fallback to Server
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"CDNOriginHost\": \"origin-fallback.org\""))
    }

    @Test
    fun `Transport defaults to CDN when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = null, // should default to CDN
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443"
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"Transport\": \"CDN\""))
    }

    @Test
    fun `ProxyMethod defaults to shadowsocks when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            ProxyMethod = null // should default to shadowsocks
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"ProxyMethod\": \"shadowsocks\""))
    }

    @Test
    fun `NumConn defaults to 8 when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            NumConn = null // should default to 8
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"NumConn\": 8"))
    }

    @Test
    fun `StreamTimeout defaults to 300 when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            StreamTimeout = null // should default to 300
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertTrue(cloakRepo.cloakConfigValue.contains("\"StreamTimeout\": 300"))
    }

    @Test
    fun `BrowserSig omitted from JSON when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            BrowserSig = null // should be omitted
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertFalse(cloakRepo.cloakConfigValue.contains("BrowserSig"))
    }

    @Test
    fun `CDNWsUrlPath omitted from JSON when not provided`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "aes-gcm",
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443",
            CDNWsUrlPath = null // should be omitted
        )

        applier.apply(outline, cloakEnabled = true)

        assertTrue(cloakRepo.cloakEnabledValue)
        assertFalse(cloakRepo.cloakConfigValue.contains("CDNWsUrlPath"))
    }

    @Test
    fun `whitespace-only required fields are treated as missing`() {
        val cloakRepo = FakeCloakRepo()
        val applier = CloakTomlApplier(cloakRepo, createTestLogger())

        val outline = OutlineConfig(
            Server = "example.org",
            Port = 443,
            Cloak = true,
            Transport = "CDN",
            EncryptionMethod = "   ", // whitespace only
            UID = "test-uid",
            PublicKey = "test-public-key",
            RemoteHost = "remote.example.org",
            RemotePort = "8443"
        )

        applier.apply(outline, cloakEnabled = true)

        assertFalse(cloakRepo.cloakEnabledValue)
        assertEquals("", cloakRepo.cloakConfigValue)
    }
}

private class FakeCloakRepo(
    initialCloakConfig: String = "",
    initialCloakEnabled: Boolean = false,
    initialCloakLocalPort: Int = 0,
) : DobbyConfigsRepositoryCloak {
    var cloakConfigValue: String = initialCloakConfig
    var cloakEnabledValue: Boolean = initialCloakEnabled
    var cloakLocalPortValue: Int = initialCloakLocalPort

    override fun getCloakConfig(): String = cloakConfigValue
    override fun setCloakConfig(newConfig: String) { cloakConfigValue = newConfig }
    override fun getIsCloakEnabled(): Boolean = cloakEnabledValue
    override fun setIsCloakEnabled(isCloakEnabled: Boolean) { cloakEnabledValue = isCloakEnabled }
    override fun getCloakLocalPort(): Int = cloakLocalPortValue
    override fun setCloakLocalPort(port: Int) { cloakLocalPortValue = port }
}
