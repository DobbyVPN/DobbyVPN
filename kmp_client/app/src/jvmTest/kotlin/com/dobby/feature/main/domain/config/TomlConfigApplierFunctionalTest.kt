package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TomlConfigApplierFunctionalTest {

    @Test
    fun `blank config returns false and does not enable outline`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = createApplier(outlineRepo, cloakRepo)

        val applied = applier.apply("   ")

        assertFalse(applied)
        assertFalse(outlineRepo.isOutlineEnabled)
        assertFalse(cloakRepo.isCloakEnabled)
    }

    @Test
    fun `missing outline section returns false and clears both configs`() {
        val outlineRepo = FakeOutlineRepo(
            isOutlineEnabled = true,
            methodPassword = "old",
            serverPort = "old:1",
        )
        val cloakRepo = FakeCloakRepo(
            isCloakEnabled = true,
            cloakConfig = "old-json"
        )
        val applier = createApplier(outlineRepo, cloakRepo)

        val applied = applier.apply("""Description = "only meta"""")

        assertFalse(applied)
        assertFalse(outlineRepo.isOutlineEnabled)
        assertEquals("", outlineRepo.methodPassword)
        assertEquals("", outlineRepo.serverPort)
        assertFalse(cloakRepo.isCloakEnabled)
        assertEquals("", cloakRepo.cloakConfig)
    }

    @Test
    fun `outline without password returns false and disables outline and cloak`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = createApplier(outlineRepo, cloakRepo)

        val applied = applier.apply(
            """
            [Outline]
            Server = "example.org"
            Port = 443
            Method = "chacha20-ietf-poly1305"
            """.trimIndent()
        )

        assertFalse(applied)
        assertFalse(outlineRepo.isOutlineEnabled)
        assertEquals("", outlineRepo.methodPassword)
        assertFalse(cloakRepo.isCloakEnabled)
    }

    @Test
    fun `valid outline applies config and keeps cloak disabled`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = createApplier(outlineRepo, cloakRepo)

        val applied = applier.apply(
            """
            [Outline]
            Server = "example.org"
            Port = 8443
            Method = "chacha20-ietf-poly1305"
            Password = "secret-pass"
            WebSocket = true
            WebSocketPath = "/ws"
            DisguisePrefix = "POST "
            """.trimIndent()
        )

        assertTrue(applied)
        assertTrue(outlineRepo.isOutlineEnabled)
        assertEquals("chacha20-ietf-poly1305:secret-pass", outlineRepo.methodPassword)
        assertEquals("example.org:8443", outlineRepo.serverPort)
        assertTrue(outlineRepo.websocketEnabled)
        assertEquals("/ws/tcp", outlineRepo.tcpPath)
        assertEquals("/ws/udp", outlineRepo.udpPath)
        assertEquals("POST ", outlineRepo.prefix)
        assertFalse(cloakRepo.isCloakEnabled)
        assertEquals("", cloakRepo.cloakConfig)
    }

    @Test
    fun `cloak enabled but invalid required cloak fields keeps outline and disables cloak`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val applier = createApplier(outlineRepo, cloakRepo)

        val applied = applier.apply(
            """
            [Outline]
            Server = "example.org"
            Port = 443
            Method = "chacha20-ietf-poly1305"
            Password = "secret-pass"
            Cloak = true
            """.trimIndent()
        )

        assertTrue(applied)
        assertTrue(outlineRepo.isOutlineEnabled)
        assertEquals("127.0.0.1:1984", outlineRepo.serverPort)
        assertFalse(cloakRepo.isCloakEnabled)
        assertEquals("", cloakRepo.cloakConfig)
    }

    @Test
    fun `logs are emitted for key config transitions`() {
        val outlineRepo = FakeOutlineRepo()
        val cloakRepo = FakeCloakRepo()
        val logEventsChannel = LogEventsChannel()
        val logsRepository = LogsRepository(logEventsChannel = logEventsChannel)
        val logger = Logger(logsRepository)
        val applier = TomlConfigApplier(outlineRepo, cloakRepo, logger)

        val applied = applier.apply(
            """
            [Outline]
            Server = "example.org"
            Port = 443
            Password = "secret-pass"
            """.trimIndent()
        )

        val allLogs = logsRepository.readAllLogs()
        assertTrue(applied)
        assertTrue(allLogs.any { it.contains("Start parseToml()") })
        assertTrue(allLogs.any { it.contains("Detected [Outline] config") })
        assertTrue(allLogs.any { it.contains("Finish parseToml()") })
    }

    private fun createApplier(
        outlineRepo: DobbyConfigsRepositoryOutline,
        cloakRepo: DobbyConfigsRepositoryCloak
    ): TomlConfigApplier {
        val logger = Logger(LogsRepository(logEventsChannel = LogEventsChannel()))
        return TomlConfigApplier(outlineRepo, cloakRepo, logger)
    }
}

private data class FakeOutlineRepo(
    var methodPassword: String = "",
    var serverPort: String = "",
    var isOutlineEnabled: Boolean = false,
    var prefix: String = "",
    var websocketEnabled: Boolean = false,
    var tcpPath: String = "",
    var udpPath: String = "",
) : DobbyConfigsRepositoryOutline {
    override fun setServerPortOutline(newConfig: String) {
        serverPort = newConfig
    }

    override fun setMethodPasswordOutline(newConfig: String) {
        methodPassword = newConfig
    }

    override fun getServerPortOutline(): String = serverPort

    override fun getMethodPasswordOutline(): String = methodPassword

    override fun getIsOutlineEnabled(): Boolean = isOutlineEnabled

    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        this.isOutlineEnabled = isOutlineEnabled
    }

    override fun getPrefixOutline(): String = prefix

    override fun setPrefixOutline(prefix: String) {
        this.prefix = prefix
    }

    override fun getIsWebsocketEnabled(): Boolean = websocketEnabled

    override fun setIsWebsocketEnabled(enabled: Boolean) {
        websocketEnabled = enabled
    }

    override fun getTcpPathOutline(): String = tcpPath

    override fun setTcpPathOutline(tcpPath: String) {
        this.tcpPath = tcpPath
    }

    override fun getUdpPathOutline(): String = udpPath

    override fun setUdpPathOutline(udpPath: String) {
        this.udpPath = udpPath
    }
}

private data class FakeCloakRepo(
    var cloakConfig: String = "",
    var isCloakEnabled: Boolean = false,
    var cloakLocalPort: Int = 0,
) : DobbyConfigsRepositoryCloak {
    override fun getCloakConfig(): String = cloakConfig

    override fun setCloakConfig(newConfig: String) {
        cloakConfig = newConfig
    }

    override fun getIsCloakEnabled(): Boolean = isCloakEnabled

    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        this.isCloakEnabled = isCloakEnabled
    }

    override fun getCloakLocalPort(): Int = cloakLocalPort

    override fun setCloakLocalPort(port: Int) {
        cloakLocalPort = port
    }
}
