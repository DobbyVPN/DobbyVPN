package com.dobby.domain

import com.dobby.feature.main.domain.DobbyConfigsRepository
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

/** Persists only the user-entered connection source URL. */
internal class DobbyConfigsRepositoryImpl(
    private val prefs: Preferences = Preferences.userRoot(),
    private val storageDir: Path = Paths.get(System.getProperty("user.home") ?: ".", ".myapp", "configs"),
) : DobbyConfigsRepository {
    private val connectionUrlFile = storageDir.resolve("connection-url.txt")

    init {
        Files.createDirectories(storageDir)
        restrictToOwner(storageDir, directory = true)
        // Migrate the one retained source value and delete all obsolete cached
        // configuration/profile/telemetry state without reading it into logs.
        if (!Files.exists(connectionUrlFile)) {
            prefs.get("connectionURL", null)?.let { writeOwnerOnly(connectionUrlFile, it) }
        }
        prefs.remove("connectionURL")
        listOf(
            "сonnectionURL", "connectionConfig", "сonnectionConfig", "connectionProfiles",
            "activeConnectionProfileIndex", "vpnInterface", "geoRoutingConf",
            "telemetryEndpoint", "telemetryApiToken", "telemetryAttributes",
        ).forEach { prefs.remove(it) }
        listOf(
            "connection-config.toml", "connection-profiles.json", "geo-routing-conf.txt",
            "telemetry-endpoint.txt", "telemetry-token.txt", "telemetry-attributes.txt",
        ).forEach { Files.deleteIfExists(storageDir.resolve(it)) }
        prefs.flush()
        if (Files.exists(connectionUrlFile)) restrictToOwner(connectionUrlFile, directory = false)
    }

    override fun getConnectionURL(): String =
        if (Files.exists(connectionUrlFile)) Files.readString(connectionUrlFile, StandardCharsets.UTF_8) else ""

    override fun setConnectionURL(connectionURL: String) {
        if (connectionURL.isEmpty()) Files.deleteIfExists(connectionUrlFile)
        else writeOwnerOnly(connectionUrlFile, connectionURL)
    }

    private fun writeOwnerOnly(file: Path, value: String) {
        Files.createDirectories(storageDir)
        restrictToOwner(storageDir, directory = true)
        val temporary = Files.createTempFile(storageDir, ".dobby-source-", ".tmp")
        try {
            restrictToOwner(temporary, directory = false)
            Files.writeString(temporary, value, StandardCharsets.UTF_8)
            runCatching {
                Files.move(temporary, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
            }.getOrElse { Files.move(temporary, file, StandardCopyOption.REPLACE_EXISTING) }
            restrictToOwner(file, directory = false)
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun restrictToOwner(path: Path, directory: Boolean) {
        if (runCatching {
                Files.setPosixFilePermissions(path, PosixFilePermissions.fromString(if (directory) "rwx------" else "rw-------"))
            }.isSuccess
        ) return
        val view = checkNotNull(Files.getFileAttributeView(path, AclFileAttributeView::class.java)) {
            "Owner-only file permissions are unavailable"
        }
        val owner = Files.getOwner(path)
        view.acl = listOf(
            AclEntry.newBuilder()
                .setType(AclEntryType.ALLOW)
                .setPrincipal(owner)
                .setPermissions(AclEntryPermission.entries.toSet())
                .apply {
                    if (directory) setFlags(AclEntryFlag.DIRECTORY_INHERIT, AclEntryFlag.FILE_INHERIT)
                }
                .build(),
        )
    }
}
