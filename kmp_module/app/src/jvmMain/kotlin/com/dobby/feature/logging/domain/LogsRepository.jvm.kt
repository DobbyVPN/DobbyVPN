package com.dobby.feature.logging.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import java.io.File
import java.nio.file.AccessDeniedException
import java.nio.file.FileAlreadyExistsException
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.BasicFileAttributes
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.UserPrincipal
import java.util.concurrent.TimeUnit

actual val fileSystem: FileSystem = FileSystem.SYSTEM
private val logWriteLock = Any()
private const val aclProtectionTimeoutSeconds = 15L
private const val aclProtectionTerminationSeconds = 5L
private const val maximumIdentityOutputBytes = 4_096
private val windowsSid = Regex("^S-1-(?:[0-9]+-){1,14}[0-9]+$")
private val localSystemAccountName: String by lazy(::readLocalSystemAccountName)

actual fun <T> withLogWriteLock(block: () -> T): T = synchronized(logWriteLock, block)

actual fun provideLogFilePath(): Path = provideLogFile("app_logs.txt")

actual fun provideGoLogFilePath(): Path = provideLogFile("go_desktop_service_logs.jsonl")

actual fun provideAdditionalLogFilePaths(): List<Path> = listOf(provideGoLogFilePath())

actual fun platformLogStorageInitializationAvailable(): Boolean = true

internal class LocalLogStorageInitializationException(
    val stage: String,
    cause: Throwable,
) : IllegalStateException(cause)

@Suppress("TooGenericExceptionCaught")
private inline fun <T> localLogStorageStage(stage: String, block: () -> T): T = try {
    block()
} catch (error: LocalLogStorageInitializationException) {
    throw error
} catch (error: Exception) {
    throw LocalLogStorageInitializationException(stage, error)
}

private fun provideLogFile(name: String): Path {
    val target = when (name) {
        "app_logs.txt" -> "app"
        "go_desktop_service_logs.jsonl" -> "service"
        else -> error("Unsupported local log producer")
    }
    val userHome = System.getProperty("user.home") ?: error("Unable to get user home directory")
    val appDir = File(userHome, ".myapp")
    val logFile = File(appDir, name)
    localLogStorageStage("$target.directory_create") {
        Files.createDirectories(appDir.toPath())
    }
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
        localLogStorageStage("$target.directory_acl") {
            restrictWithAcl(appDir.toPath(), directory = true)
        }
        localLogStorageStage("$target.file_create") {
            ensureLogFileEntry(appDir.toPath(), logFile.toPath())
        }
        localLogStorageStage("$target.file_acl") {
            restrictWithAcl(logFile.toPath(), directory = false)
        }
        localLogStorageStage("$target.file_type") {
            verifyRegularLogFile(logFile.toPath())
        }
    }
    localLogStorageStage("$target.file_access") {
        verifyLogFileAccessible(logFile.toPath())
    }
    return logFile.absolutePath.toPath()
}

/**
 * Finds an existing child from the directory rather than querying the child
 * itself. A prior DobbyVPN version could leave a real Windows log file with an
 * empty DACL; querying that file then reports it as missing even though a
 * create attempt fails with "already exists". The directory remains owned and
 * accessible, so discover the entry there and let [restrictWithAcl] repair it.
 * If metadata is readable before repair, reject a non-file immediately. An
 * access-denied metadata read is expected for the historical empty-DACL state;
 * the caller performs the same strict type check after repairing the DACL.
 */
internal fun ensureLogFileEntry(directory: java.nio.file.Path, logFile: java.nio.file.Path) {
    require(logFile.parent == directory) { "Local log storage must be a direct child" }
    val entryExists = Files.newDirectoryStream(directory).use { entries ->
        entries.any { it.fileName == logFile.fileName }
    }
    if (!entryExists) {
        try {
            Files.createFile(logFile)
        } catch (_: FileAlreadyExistsException) {
            // A second local process may have created the same fixed file
            // after the directory snapshot. Validate and secure that entry.
        }
    }
    try {
        verifyRegularLogFile(logFile)
    } catch (_: AccessDeniedException) {
        // Repair the known existing entry before requiring readable metadata.
    }
}

private fun verifyRegularLogFile(logFile: java.nio.file.Path) {
    val attributes = Files.readAttributes(logFile, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    if (!attributes.isRegularFile) error("Local log storage is not a regular file")
}

private fun restrictWithAcl(path: java.nio.file.Path, directory: Boolean) {
    val view = Files.getFileAttributeView(path, java.nio.file.attribute.AclFileAttributeView::class.java)
        ?: error("Owner-only file permissions are unavailable")
    val lookup = path.fileSystem.userPrincipalLookupService
    val currentUser = if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) {
        setWindowsOwner(path, currentWindowsUserSid())
        Files.getOwner(path)
    } else {
        Files.getOwner(path)
    }
    val principals = buildList {
        add(currentUser)
        // The Windows tunnel service runs as SYSTEM and appends to this same
        // local log. No other identity is granted access.
        if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) {
            add(lookup.lookupPrincipalByName(localSystemAccountName))
        }
    }.distinctBy { it.name }
    val expectedAcl = principals.map { principal ->
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
    // Preserve access before replacing inherited entries with the exact owner
    // + SYSTEM policy. Java's ACL write can re-enable inheritance for a file,
    // so protect the final DACL again without removing either explicit entry.
    disableWindowsAclInheritancePreservingAccess(path)
    view.owner = currentUser
    view.acl = expectedAcl
    disableWindowsAclInheritancePreservingAccess(path)
    verifyAcl(view, currentUser, expectedAcl)
}

private fun verifyAcl(
    view: java.nio.file.attribute.AclFileAttributeView,
    currentUser: UserPrincipal,
    expectedAcl: List<java.nio.file.attribute.AclEntry>,
) {
    if (view.owner != currentUser || view.acl.toSet() != expectedAcl.toSet() || view.acl.size != expectedAcl.size) {
        error("Owner-only Windows ACL verification failed")
    }
}

private fun verifyLogFileAccessible(path: java.nio.file.Path) {
    Files.newByteChannel(path, StandardOpenOption.READ, StandardOpenOption.WRITE).use { }
}

private fun currentWindowsUserSid(): String {
    val systemRoot = windowsSystemRoot()
    val whoami = File(systemRoot, "System32/whoami.exe")
    if (!whoami.isFile) error("Windows identity tool is unavailable")
    val process = ProcessBuilder(whoami.absolutePath, "/user", "/fo", "csv", "/nh")
        .redirectError(ProcessBuilder.Redirect.DISCARD)
        .start()
    if (!process.waitFor(aclProtectionTimeoutSeconds, TimeUnit.SECONDS)) {
        process.destroyForcibly()
        process.waitFor(aclProtectionTerminationSeconds, TimeUnit.SECONDS)
        error("Windows identity lookup timed out")
    }
    val output = process.inputStream.readBytes()
    if (process.exitValue() != 0 || output.size > maximumIdentityOutputBytes) {
        output.fill(0)
        error("Windows identity lookup failed")
    }
    return try {
        parseWindowsUserSid(output.toString(Charsets.ISO_8859_1))
    } finally {
        output.fill(0)
    }
}

internal fun parseWindowsUserSid(value: String): String {
    val match = Regex("^\\\"(?:[^\\\"]|\\\"\\\")*\\\",\\\"(S-1-(?:[0-9]+-){1,14}[0-9]+)\\\"\\s*$").matchEntire(value)
        ?: error("Windows identity output is invalid")
    return match.groupValues[1].takeIf(windowsSid::matches)
        ?: error("Windows identity output is invalid")
}

private fun readLocalSystemAccountName(): String {
    val powershell = File(windowsSystemRoot(), "System32/WindowsPowerShell/v1.0/powershell.exe")
    if (!powershell.isFile) error("Windows identity translation tool is unavailable")
    val command = "[Console]::OutputEncoding=[Text.UTF8Encoding]::new(\$false);" +
        "\$sid=[Security.Principal.SecurityIdentifier]::new('S-1-5-18');" +
        "[Console]::Write(\$sid.Translate([Security.Principal.NTAccount]).Value)"
    val process = ProcessBuilder(
        powershell.absolutePath,
        "-NoLogo",
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        command,
    ).redirectError(ProcessBuilder.Redirect.DISCARD).start()
    if (!process.waitFor(aclProtectionTimeoutSeconds, TimeUnit.SECONDS)) {
        process.destroyForcibly()
        process.waitFor(aclProtectionTerminationSeconds, TimeUnit.SECONDS)
        error("Windows identity translation timed out")
    }
    val output = process.inputStream.readBytes()
    if (process.exitValue() != 0 || output.size > maximumIdentityOutputBytes) {
        output.fill(0)
        error("Windows identity translation failed")
    }
    return try {
        parseWindowsAccountName(output.toString(Charsets.UTF_8))
    } finally {
        output.fill(0)
    }
}

internal fun parseWindowsAccountName(value: String): String = value.takeIf {
    it.length in 3..256 && '\\' in it && it.none(Char::isISOControl)
} ?: error("Windows account identity output is invalid")

private fun setWindowsOwner(path: java.nio.file.Path, sid: String) {
    val icacls = File(windowsSystemRoot(), "System32/icacls.exe")
    if (!icacls.isFile) error("Windows ACL protection tool is unavailable")
    runWindowsAclTool(icacls, path, "/setowner", "*$sid")
}

private fun windowsSystemRoot(): File = System.getenv("SystemRoot")
    ?.let(::File)
    ?.takeIf { it.isAbsolute }
    ?: error("Windows system directory is unavailable")

private fun disableWindowsAclInheritancePreservingAccess(path: java.nio.file.Path) {
    if (!System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) return
    val icacls = File(windowsSystemRoot(), "System32/icacls.exe")
    if (!icacls.isFile) error("Windows ACL protection tool is unavailable")
    runWindowsAclTool(icacls, path, "/inheritance:d")
}

private fun runWindowsAclTool(icacls: File, path: java.nio.file.Path, vararg arguments: String) {
    val process = ProcessBuilder(icacls.absolutePath, path.toString(), *arguments)
        .redirectOutput(ProcessBuilder.Redirect.DISCARD)
        .redirectError(ProcessBuilder.Redirect.DISCARD)
        .start()
    if (!process.waitFor(aclProtectionTimeoutSeconds, TimeUnit.SECONDS)) {
        process.destroyForcibly()
        process.waitFor(aclProtectionTerminationSeconds, TimeUnit.SECONDS)
        error("Windows ACL protection timed out")
    }
    if (process.exitValue() != 0) error("Windows ACL protection failed")
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
