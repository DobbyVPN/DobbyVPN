package com.dobby.feature.logging.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import java.io.File
import java.nio.file.Files
import java.nio.file.attribute.PosixFilePermission

actual val fileSystem: FileSystem = FileSystem.SYSTEM
private val logWriteLock = Any()

actual fun <T> withLogWriteLock(block: () -> T): T = synchronized(logWriteLock, block)

actual fun provideLogFilePath(): Path = provideLogFile("app_logs.txt")

actual fun provideGoLogFilePath(): Path = provideLogFile("go_desktop_service_logs.jsonl")

actual fun provideAdditionalLogFilePaths(): List<Path> = listOf(provideGoLogFilePath())

private fun provideLogFile(name: String): Path {
    val userHome = System.getProperty("user.home") ?: error("Unable to get user home directory")
    val appDir = File(userHome, ".myapp")
    val logFile = File(appDir, name)
    Files.createDirectories(appDir.toPath())
    val posixSecured = runCatching {
        Files.setPosixFilePermissions(
            appDir.toPath(),
            setOf(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE, PosixFilePermission.OWNER_EXECUTE),
        )
        if (!logFile.exists()) Files.createFile(logFile.toPath())
        Files.setPosixFilePermissions(
            logFile.toPath(),
            setOf(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_WRITE),
        )
    }.isSuccess
    if (!posixSecured) {
        restrictWithAcl(appDir.toPath(), directory = true)
        if (!logFile.exists()) Files.createFile(logFile.toPath())
        restrictWithAcl(logFile.toPath(), directory = false)
    }
    return logFile.absolutePath.toPath()
}

private fun restrictWithAcl(path: java.nio.file.Path, directory: Boolean) {
    val view = Files.getFileAttributeView(path, java.nio.file.attribute.AclFileAttributeView::class.java)
        ?: error("Owner-only file permissions are unavailable for $path")
    val principals = buildList {
        add(Files.getOwner(path))
        // The Windows tunnel service runs as SYSTEM and appends to this same
        // local log. No other identity is granted access.
        if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) {
            add(path.fileSystem.userPrincipalLookupService.lookupPrincipalByName("SYSTEM"))
        }
    }.distinctBy { it.name }
    view.acl = principals.map { principal ->
        java.nio.file.attribute.AclEntry.newBuilder()
            .setType(java.nio.file.attribute.AclEntryType.ALLOW)
            .setPrincipal(principal)
            .setPermissions(java.nio.file.attribute.AclEntryPermission.entries.toSet())
            .apply {
                if (directory) {
                    setFlags(
                        java.nio.file.attribute.AclEntryFlag.DIRECTORY_INHERIT,
                        java.nio.file.attribute.AclEntryFlag.FILE_INHERIT,
                    )
                }
            }
            .build()
    }
}

actual fun platformLogInfo(): String {
    return "platform=jvm " +
        "osName=${System.getProperty("os.name")} " +
        "osVersion=${System.getProperty("os.version")} " +
        "osArch=${System.getProperty("os.arch")} " +
        "javaVersion=${System.getProperty("java.version")} " +
        "javaVendor=${System.getProperty("java.vendor")} " +
        "javaVm=${System.getProperty("java.vm.name")}"
}
