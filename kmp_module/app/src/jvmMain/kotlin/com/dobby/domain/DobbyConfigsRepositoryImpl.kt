package com.dobby.domain

import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import interop.healthcheck.HealthCheckLibrary
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.nio.file.StandardCopyOption
import java.nio.file.attribute.AclEntry
import java.nio.file.attribute.AclEntryFlag
import java.nio.file.attribute.AclEntryPermission
import java.nio.file.attribute.AclEntryType
import java.nio.file.attribute.AclFileAttributeView
import java.nio.file.attribute.PosixFilePermissions
import java.util.prefs.Preferences

internal class DobbyConfigsRepositoryImpl(
    private val prefs: Preferences = Preferences.userRoot(),
    private val healthCheckLibrary: HealthCheckLibrary,
    private val storageDir: Path = Paths.get(
        System.getProperty("user.home") ?: ".",
        ".myapp",
        "configs"
    ),
) : DobbyConfigsRepository {
    private val connectionUrlFile = storageDir.resolve("connection-url.txt")
    private val connectionConfigFile = storageDir.resolve("connection-config.toml")
    private val connectionProfilesFile = storageDir.resolve("connection-profiles.json")
    private val geoRoutingConfFile = storageDir.resolve("geo-routing-conf.txt")
    private val prefixOutlineFile = storageDir.resolve("prefix-outline.txt")
    private val secretFiles = mapOf(
        "telemetryEndpoint" to storageDir.resolve("telemetry-endpoint.txt"),
        "telemetryApiToken" to storageDir.resolve("telemetry-token.txt"),
        "telemetryAttributes" to storageDir.resolve("telemetry-attributes.txt"),
        "cloakConfig" to storageDir.resolve("cloak-config.json"),
        "ServerPortKey" to storageDir.resolve("outline-server.txt"),
        "MethodPasswordOutlineKey" to storageDir.resolve("outline-credentials.txt"),
        "TcpPathOutlineKey" to storageDir.resolve("outline-tcp-path.txt"),
        "UdpPathOutlineKey" to storageDir.resolve("outline-udp-path.txt"),
        "xrayConfig" to storageDir.resolve("xray-config.json"),
        "trustTunnelConfig" to storageDir.resolve("trusttunnel-config.toml"),
    )

    init {
        Files.createDirectories(storageDir)
        restrictToOwner(storageDir, directory = true)
        val allFiles = secretFiles + mapOf(
            "connectionURL" to connectionUrlFile,
            "connectionConfig" to connectionConfigFile,
            "connectionProfiles" to connectionProfilesFile,
            "geoRoutingConf" to geoRoutingConfFile,
            "PrefixOutlineKey" to prefixOutlineFile,
        )
        allFiles.forEach { (key, file) ->
            if (!Files.exists(file)) {
                prefs.get(key, null)?.let { writeOwnerOnly(file, it) }
            } else {
                restrictToOwner(file, directory = false)
            }
            prefs.remove(key)
        }
        prefs.flush()
    }

    private fun readLargeString(file: Path): String {
        return if (Files.exists(file)) {
            Files.readString(file, StandardCharsets.UTF_8)
        } else {
            ""
        }
    }

    private fun writeLargeString(key: String, file: Path, value: String) {
        if (value.isEmpty()) {
            Files.deleteIfExists(file)
        } else {
            writeOwnerOnly(file, value)
        }
        prefs.remove(key)
    }

    private fun readSecret(key: String): String = readLargeString(secretFiles.getValue(key))
    private fun writeSecret(key: String, value: String) = writeLargeString(key, secretFiles.getValue(key), value)

    override fun getVpnInterface(): VpnInterface {
        val prefsResult = prefs.get("vpnInterface", VpnInterface.DEFAULT_VALUE.toString())
            ?: VpnInterface.DEFAULT_VALUE.toString()

        return VpnInterface.valueOf(prefsResult)
    }

    override fun setVpnInterface(vpnInterface: VpnInterface) {
        prefs.put("vpnInterface", vpnInterface.toString())
    }

    override fun getConnectionURL(): String {
        return readLargeString(connectionUrlFile)
    }

    override fun setConnectionURL(connectionURL: String) {
        writeLargeString("connectionURL", connectionUrlFile, connectionURL)
    }

    override fun getConnectionConfig(): String {
        return readLargeString(connectionConfigFile)
    }

    override fun setConnectionConfig(connectionConfig: String) {
        writeLargeString("connectionConfig", connectionConfigFile, connectionConfig)
    }

    override fun getConnectionProfiles(): String {
        return readLargeString(connectionProfilesFile)
    }

    override fun setConnectionProfiles(connectionProfiles: String) {
        writeLargeString("connectionProfiles", connectionProfilesFile, connectionProfiles)
    }

    override fun getActiveConnectionProfileIndex(): Int {
        return prefs.get("activeConnectionProfileIndex", "0").toIntOrNull() ?: 0
    }

    override fun setActiveConnectionProfileIndex(index: Int) {
        prefs.put("activeConnectionProfileIndex", index.toString())
    }

    override fun getTelemetryEndpoint(): String {
        return readSecret("telemetryEndpoint")
    }

    override fun setTelemetryEndpoint(endpoint: String) {
        writeSecret("telemetryEndpoint", endpoint)
    }

    override fun getTelemetryApiToken(): String {
        return readSecret("telemetryApiToken")
    }

    override fun setTelemetryApiToken(token: String) {
        writeSecret("telemetryApiToken", token)
    }

    override fun getTelemetryAttributes(): String {
        return readSecret("telemetryAttributes")
    }

    override fun setTelemetryAttributes(config: String) {
        writeSecret("telemetryAttributes", config)
    }

    override fun getCloakConfig(): String {
        return readSecret("cloakConfig")
    }

    override fun setCloakConfig(newConfig: String) {
        writeSecret("cloakConfig", newConfig)
    }

    override fun getIsCloakEnabled(): Boolean {
        return prefs.get("isCloakEnabled", "false").equals("true")
    }

    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        prefs.put("isCloakEnabled", isCloakEnabled.toString())
    }

    override fun getCloakLocalPort(): Int {
        return prefs.get("cloakLocalPort", "1984").toIntOrNull() ?: 1984
    }

    override fun setCloakLocalPort(port: Int) {
        prefs.put("cloakLocalPort", port.toString())
    }

    override fun setServerPort(newConfig: String) {
        writeSecret("ServerPortKey", newConfig)
    }

    override fun setMethodPasswordOutline(newConfig: String) {
        writeSecret("MethodPasswordOutlineKey", newConfig)
    }

    override fun getServerPort(): String {
        return readSecret("ServerPortKey")
    }

    override fun getMethodPasswordOutline(): String {
        return readSecret("MethodPasswordOutlineKey")
    }

    override fun getIsOutlineEnabled(): Boolean {
        return prefs.get("isOutlineEnabled", "false").equals("true")
    }

    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        prefs.put("isOutlineEnabled", isOutlineEnabled.toString())
    }

    override fun getPrefixOutline(): String {
        return readLargeString(prefixOutlineFile)
    }

    override fun setPrefixOutline(prefix: String) {
        writeLargeString("PrefixOutlineKey", prefixOutlineFile, prefix)
    }

    override fun getTcpPathOutline(): String {
        return readSecret("TcpPathOutlineKey")
    }

    override fun setTcpPathOutline(tcpPath: String) {
        writeSecret("TcpPathOutlineKey", tcpPath)
    }

    override fun getIsWebsocketEnabled(): Boolean {
        return prefs.get("isWebsocketEnabled", "false").equals("true")
    }

    override fun setIsWebsocketEnabled(enabled: Boolean) {
        prefs.put("isWebsocketEnabled", enabled.toString())
    }

    override fun getUdpPathOutline(): String {
        return readSecret("UdpPathOutlineKey")
    }

    override fun setUdpPathOutline(udpPath: String) {
        writeSecret("UdpPathOutlineKey", udpPath)
    }

    override fun getXrayConfig(): String {
        return readSecret("xrayConfig")
    }

    override fun setXrayConfig(config: String) {
        writeSecret("xrayConfig", config)
    }

    override fun getIsXrayEnabled(): Boolean {
        return prefs.get("isXrayEnabled", "false").equals("true")
    }

    override fun setIsXrayEnabled(isXrayEnabled: Boolean) {
        if (isXrayEnabled) {
            setVpnInterface(VpnInterface.XRAY) // TODO (find other place for this command?)
        } else {
            setVpnInterface(VpnInterface.DEFAULT_VALUE)
        }
        prefs.put("isXrayEnabled", isXrayEnabled.toString())
    }

    override fun getTrustTunnelConfig(): String = readSecret("trustTunnelConfig")

    override fun setTrustTunnelConfig(config: String) {
        writeSecret("trustTunnelConfig", config)
    }

    override fun getIsTrustTunnelEnabled(): Boolean {
        return prefs.get("isTrustTunnelEnabled", "false").equals("true")
    }

    override fun setIsTrustTunnelEnabled(isTrustTunnelEnabled: Boolean) {
        if (isTrustTunnelEnabled) {
            setVpnInterface(VpnInterface.TRUST_TUNNEL)
        } else {
            setVpnInterface(VpnInterface.DEFAULT_VALUE)
        }
        prefs.put("isTrustTunnelEnabled", isTrustTunnelEnabled.toString())
    }

    override fun couldStart(): Boolean {
        return healthCheckLibrary.CouldStart()
    }

    override fun getGeoRoutingConf(): String {
        return readLargeString(geoRoutingConfFile)
    }

    override fun setGeoRoutingConf(geoRoutingConf: String) {
        writeLargeString("geoRoutingConf", geoRoutingConfFile, geoRoutingConf)
    }

    private fun writeOwnerOnly(file: Path, value: String) {
        Files.createDirectories(storageDir)
        restrictToOwner(storageDir, directory = true)
        val temporary = Files.createTempFile(storageDir, ".dobby-secret-", ".tmp")
        try {
            restrictToOwner(temporary, directory = false)
            Files.writeString(temporary, value, StandardCharsets.UTF_8)
            runCatching {
                Files.move(
                    temporary,
                    file,
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING,
                )
            }.getOrElse {
                Files.move(temporary, file, StandardCopyOption.REPLACE_EXISTING)
            }
            restrictToOwner(file, directory = false)
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun restrictToOwner(path: Path, directory: Boolean) {
        val permissions = if (directory) "rwx------" else "rw-------"
        if (!runCatching {
                Files.setPosixFilePermissions(path, PosixFilePermissions.fromString(permissions))
            }.isSuccess
        ) {
            val view = checkNotNull(Files.getFileAttributeView(path, AclFileAttributeView::class.java)) {
                "Owner-only file permissions are unavailable for $path"
            }
            val owner = Files.getOwner(path)
            val entry = AclEntry.newBuilder()
                .setType(AclEntryType.ALLOW)
                .setPrincipal(owner)
                .setPermissions(AclEntryPermission.entries.toSet())
                .apply {
                    if (directory) {
                        setFlags(AclEntryFlag.DIRECTORY_INHERIT, AclEntryFlag.FILE_INHERIT)
                    }
                }
                .build()
            view.acl = listOf(entry)
        }
    }
}
