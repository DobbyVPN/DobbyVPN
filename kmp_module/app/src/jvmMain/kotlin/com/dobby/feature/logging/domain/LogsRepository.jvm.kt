package com.dobby.feature.logging.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import java.io.ByteArrayOutputStream
import java.io.File
import java.nio.channels.FileChannel
import java.nio.file.DirectoryStream
import java.nio.file.FileAlreadyExistsException
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.NoSuchFileException
import java.nio.file.SecureDirectoryStream
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.BasicFileAttributes
import java.nio.file.attribute.BasicFileAttributeView
import java.nio.file.attribute.FileAttribute
import java.nio.file.attribute.PosixFileAttributeView
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.PosixFilePermissions
import java.nio.file.attribute.UserPrincipal
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import okio.buffer
import okio.use
import kotlin.concurrent.thread

actual val fileSystem: FileSystem = FileSystem.SYSTEM
private val logWriteLock = Any()
private const val aclProtectionTimeoutSeconds = 15L
private const val aclProtectionTerminationSeconds = 5L
private const val maximumIdentityOutputBytes = 4_096
private val windowsSid = Regex("^S-1-(?:[0-9]+-){1,14}[0-9]+$")
private val localSystemAccountName: String by lazy(::readLocalSystemAccountName)
private val ownerOnlyDirectoryPermissions = setOf(
    PosixFilePermission.OWNER_READ,
    PosixFilePermission.OWNER_WRITE,
    PosixFilePermission.OWNER_EXECUTE,
)
private val ownerOnlyFilePermissions = setOf(
    PosixFilePermission.OWNER_READ,
    PosixFilePermission.OWNER_WRITE,
)

actual fun <T> withLogWriteLock(block: () -> T): T = synchronized(logWriteLock, block)

actual fun provideLogFilePath(): Path = provideLogFile("app_logs.txt")

actual fun provideGoLogFilePath(): Path = provideLogFile("go_desktop_service_logs.jsonl")

actual fun provideAdditionalLogFilePaths(): List<Path> = listOf(provideGoLogFilePath())

actual fun platformLogStorageInitializationAvailable(): Boolean = true

actual fun clearLogFile(path: Path, storageFileSystem: FileSystem) {
    if (storageFileSystem !== fileSystem) {
        storageFileSystem.sink(path).buffer().use { }
        return
    }
    clearActiveJvmLogFile(java.nio.file.Path.of(path.toString()))
}

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

/**
 * Creates only the current owner-local log under `.dobbyvpn`. The former
 * `.myapp` log tree is intentionally neither read nor modified; users start
 * with a fresh current log after this product cleanup.
 */
private fun provideLogFile(name: String): Path {
    val target = when (name) {
        "app_logs.txt" -> "app"
        "go_desktop_service_logs.jsonl" -> "service"
        else -> error("Unsupported local log producer")
    }
    val userHome = System.getProperty("user.home") ?: error("Unable to get user home directory")
    val homePath = java.nio.file.Path.of(userHome).toAbsolutePath().normalize()
    val appDirPath = homePath.resolve(".dobbyvpn")
    val logFilePath = appDirPath.resolve(name)
    val posixAvailable = Files.getFileAttributeView(homePath, PosixFileAttributeView::class.java) != null
    if (posixAvailable) {
        localLogStorageStage("$target.secure_posix_create") {
            ensureSecurePosixLogFile(homePath, appDirPath, logFilePath)
        }
    } else {
        localLogStorageStage("$target.acl_create") {
            ensureAclProtectedLogFile(homePath, appDirPath, logFilePath)
        }
    }
    localLogStorageStage("$target.file_access") {
        verifyLogFileAccessible(logFilePath)
    }
    return logFilePath.toString().toPath()
}

private fun ensureSecurePosixLogFile(
    homePath: java.nio.file.Path,
    appDirPath: java.nio.file.Path,
    logFilePath: java.nio.file.Path,
) {
    // The absolute POSIX creation boundary is the real, current-user-owned
    // home directory. Group/other writable homes are rejected before any
    // `.dobbyvpn` entry can be created.
    verifyTrustedPosixHome(homePath)
    verifyActiveLogDirectory(homePath)
    val homeBefore = Files.readAttributes(homePath, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    Files.newDirectoryStream(homePath).use { rawBase ->
        val base = rawBase.asSecureDirectory("local log base")
        val baseBefore = secureDirectoryAttributes(base)
        if (!sameActiveFile(homeBefore, baseBefore)) error("Local log base changed while opening")

        val appDirBefore = secureBasicAttributes(base, appDirPath.fileName)
        if (appDirBefore == null) {
            try {
                Files.createDirectory(appDirPath, *ownerOnlyDirectoryAttributes())
            } catch (_: FileAlreadyExistsException) {
                // A concurrent local producer won creation; validate below.
            }
        }
        val baseAfterDirectoryCreate = secureDirectoryAttributes(base)
        if (!sameActiveFile(baseBefore, baseAfterDirectoryCreate)) {
            error("Local log base changed during directory creation")
        }

        base.newDirectoryStream(appDirPath.fileName, LinkOption.NOFOLLOW_LINKS).use { rawDirectory ->
            val directory = rawDirectory.asSecureDirectory("current local log directory")
            val directoryBefore = secureDirectoryAttributes(directory)
            if (appDirBefore != null && !sameActiveFile(appDirBefore, directoryBefore)) {
                error("Current local log directory changed while opening")
            }
            val directoryView = directory.getFileAttributeView(PosixFileAttributeView::class.java)
                ?: error("POSIX local log directory attributes are unavailable")
            if (directoryView.readAttributes().permissions() != ownerOnlyDirectoryPermissions) {
                directoryView.setPermissions(ownerOnlyDirectoryPermissions)
            }
            if (directoryView.readAttributes().permissions() != ownerOnlyDirectoryPermissions) {
                error("Local log directory is not owner-only")
            }
            secureOptionalAcl(appDirPath, directoryBefore, isDirectory = true, parentDirectory = directory)

            val fileName = logFilePath.fileName
            val before = secureBasicAttributes(directory, fileName)
            if (before != null && (before.isSymbolicLink || !before.isRegularFile)) {
                error("Local log path is not a regular file")
            }
            if (before == null) {
                directory.newByteChannel(
                    fileName,
                    setOf(StandardOpenOption.WRITE, StandardOpenOption.CREATE_NEW, LinkOption.NOFOLLOW_LINKS),
                    *ownerOnlyFileAttributes(),
                ).use { }
            }
            val afterCreate = secureBasicAttributes(directory, fileName)
                ?: error("Local log file disappeared during creation")
            if (before != null && !sameActiveFile(before, afterCreate)) {
                error("Local log path changed during creation")
            }
            val fileView = directory.getFileAttributeView(
                fileName,
                PosixFileAttributeView::class.java,
                LinkOption.NOFOLLOW_LINKS,
            ) ?: error("POSIX local log file attributes are unavailable")
            if (fileView.readAttributes().permissions() != ownerOnlyFilePermissions) {
                fileView.setPermissions(ownerOnlyFilePermissions)
            }
            val afterPermissions = secureBasicAttributes(directory, fileName)
                ?: error("Local log file disappeared while securing permissions")
            if (!sameActiveFile(afterCreate, afterPermissions)) {
                error("Local log path changed while securing permissions")
            }
            if (fileView.readAttributes().permissions() != ownerOnlyFilePermissions) {
                error("Local log file is not owner-only")
            }
            secureOptionalAcl(
                logFilePath,
                afterPermissions,
                isDirectory = false,
                parentDirectory = directory,
                entryName = fileName,
            )
            if (!sameActiveFile(directoryBefore, secureDirectoryAttributes(directory))) {
                error("Local log directory changed while securing permissions")
            }
        }
        if (!sameActiveFile(baseBefore, secureDirectoryAttributes(base))) {
            error("Local log base changed while securing the current log")
        }
    }
    val homeAfter = Files.readAttributes(homePath, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    if (!sameActiveFile(homeBefore, homeAfter)) error("Local log base changed after initialization")
}

private fun ensureAclProtectedLogFile(
    homePath: java.nio.file.Path,
    appDirPath: java.nio.file.Path,
    logFilePath: java.nio.file.Path,
) {
    // Windows Java has no portable descriptor-relative directory creation.
    // Establish the real-directory and parent ACL trust boundary before any
    // path-based create; only the current owner and SYSTEM may write it.
    verifyWindowsJavaParentTrust(homePath)
    verifyActiveLogDirectory(homePath)
    val homeBefore = Files.readAttributes(homePath, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    Files.newDirectoryStream(homePath).use {
        val appDirBefore = if (Files.exists(appDirPath, LinkOption.NOFOLLOW_LINKS)) {
            activeLogDirectoryAttributes(appDirPath)
        } else {
            null
        }
        if (appDirBefore == null) {
            if (!sameActiveFile(homeBefore, activeLogDirectoryAttributes(homePath))) {
                error("Local log base changed before directory creation")
            }
            try {
                Files.createDirectory(appDirPath)
            } catch (_: FileAlreadyExistsException) {
                // A concurrent local producer won creation; validate below.
            }
        }
        verifyActiveLogDirectory(appDirPath)
        if (!sameActiveFile(homeBefore, activeLogDirectoryAttributes(homePath))) {
            error("Local log base changed after directory creation")
        }
        Files.newDirectoryStream(appDirPath).use {
            val directoryBefore = activeLogDirectoryAttributes(appDirPath)
            if (appDirBefore != null && !sameActiveFile(appDirBefore, directoryBefore)) {
                error("Local log directory changed while opening")
            }
            restrictWithAcl(appDirPath, directory = true)
            verifyWindowsJavaExactOwnerSystemAcl(appDirPath, directory = true)
            ensureLogFileEntry(appDirPath, logFilePath)
            val fileBefore = activeLogFileAttributes(logFilePath)
            restrictWithAcl(logFilePath, directory = false)
            verifyWindowsJavaExactOwnerSystemAcl(logFilePath, directory = false)
            val fileAfter = activeLogFileAttributes(logFilePath)
            if (!sameActiveFile(fileBefore, fileAfter)) error("Local log path changed while securing ACL")
            if (!sameActiveFile(directoryBefore, activeLogDirectoryAttributes(appDirPath))) {
                error("Local log directory changed while securing ACL")
            }
        }
        val homeAfter = Files.readAttributes(homePath, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
        if (!sameActiveFile(homeBefore, homeAfter)) error("Local log base changed during ACL setup")
    }
}

private fun ownerOnlyDirectoryAttributes(): Array<FileAttribute<*>> = arrayOf(
    PosixFilePermissions.asFileAttribute(ownerOnlyDirectoryPermissions),
)

private fun secureOptionalAcl(
    path: java.nio.file.Path,
    expected: BasicFileAttributes,
    isDirectory: Boolean,
    parentDirectory: SecureDirectoryStream<java.nio.file.Path>,
    entryName: java.nio.file.Path? = null,
) {
    val view = if (entryName == null) {
        parentDirectory.getFileAttributeView(java.nio.file.attribute.AclFileAttributeView::class.java)
    } else {
        parentDirectory.getFileAttributeView(
            entryName,
            java.nio.file.attribute.AclFileAttributeView::class.java,
            LinkOption.NOFOLLOW_LINKS,
        )
    } ?: return
    restrictWithAclView(path, view, isDirectory)
    val after = if (entryName == null) {
        secureDirectoryAttributes(parentDirectory)
    } else {
        secureBasicAttributes(parentDirectory, entryName) ?: error("Local log path disappeared while securing ACL")
    }
    if (!sameActiveFile(expected, after)) {
        error("Local log path changed while securing ACL")
    }
}

private fun ownerOnlyFileAttributes(): Array<FileAttribute<*>> = arrayOf(
    PosixFilePermissions.asFileAttribute(ownerOnlyFilePermissions),
)

private fun DirectoryStream<java.nio.file.Path>.asSecureDirectory(description: String): SecureDirectoryStream<java.nio.file.Path> {
    @Suppress("UNCHECKED_CAST")
    return this as? SecureDirectoryStream<java.nio.file.Path>
        ?: error("$description does not support secure descriptor-relative operations")
}

private fun secureDirectoryAttributes(directory: SecureDirectoryStream<java.nio.file.Path>): BasicFileAttributes =
    directory.getFileAttributeView(BasicFileAttributeView::class.java)?.readAttributes()
        ?: error("Secure local log directory attributes are unavailable")

private fun secureBasicAttributes(
    directory: SecureDirectoryStream<java.nio.file.Path>,
    name: java.nio.file.Path,
): BasicFileAttributes? = try {
    directory.getFileAttributeView(name, BasicFileAttributeView::class.java, LinkOption.NOFOLLOW_LINKS)
        ?.readAttributes() ?: error("Secure local log attributes are unavailable")
} catch (_: NoSuchFileException) {
    null
}

private fun verifyActiveLogDirectory(directory: java.nio.file.Path) {
    val absolute = directory.toAbsolutePath().normalize()
    val resolved = absolute.toRealPath(LinkOption.NOFOLLOW_LINKS).normalize()
    val same = if (File.separatorChar == '\\') {
        resolved.toString().equals(absolute.toString(), ignoreCase = true)
    } else {
        resolved == absolute
    }
    if (!same) error("Local log directory resolves through an alias")
    val attributes = Files.readAttributes(
        absolute,
        BasicFileAttributes::class.java,
        LinkOption.NOFOLLOW_LINKS,
    )
    if (!attributes.isDirectory || attributes.isSymbolicLink) {
        error("Local log directory is not a real directory")
    }
}

/**
 * Absolute POSIX log creation is trusted only beneath the real home directory
 * owned by the current user and not writable by group/other users.
 */
private fun verifyTrustedPosixHome(homePath: java.nio.file.Path) {
    val view = Files.getFileAttributeView(homePath, PosixFileAttributeView::class.java)
        ?: error("POSIX user-home attributes are unavailable")
    val attributes = view.readAttributes()
    if (!attributes.isDirectory || attributes.isSymbolicLink) {
        error("POSIX user home is not a real directory")
    }
    val currentUser = System.getProperty("user.name")?.takeIf { it.isNotBlank() }
        ?: error("Current POSIX user identity is unavailable")
    val owner = Files.getOwner(homePath, LinkOption.NOFOLLOW_LINKS)
    if (owner.name != currentUser) {
        error("POSIX user home is not owned by the current user")
    }
    val permissions = Files.getPosixFilePermissions(homePath, LinkOption.NOFOLLOW_LINKS)
    if (PosixFilePermission.GROUP_WRITE in permissions || PosixFilePermission.OTHERS_WRITE in permissions) {
        error("POSIX user home is writable by a group or other user")
    }
}

/**
 * Java's Windows provider has no portable directory-handle walk. Before any
 * path-based operation, require a real home directory whose write-capable ACL
 * entries are limited to trusted owner/SYSTEM/administrators identities.
 */
private fun verifyWindowsJavaParentTrust(homePath: java.nio.file.Path) {
    if (!System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) return
    verifyActiveLogDirectory(homePath)
    val view = Files.getFileAttributeView(homePath, java.nio.file.attribute.AclFileAttributeView::class.java)
        ?: error("Windows user-home ACL attributes are unavailable")
    val owner = view.owner
    val system = homePath.fileSystem.userPrincipalLookupService
        .lookupPrincipalByName(localSystemAccountName)
    val writePermissions = setOf(
        java.nio.file.attribute.AclEntryPermission.WRITE_DATA,
        java.nio.file.attribute.AclEntryPermission.APPEND_DATA,
        java.nio.file.attribute.AclEntryPermission.WRITE_ATTRIBUTES,
        java.nio.file.attribute.AclEntryPermission.WRITE_NAMED_ATTRS,
        java.nio.file.attribute.AclEntryPermission.DELETE,
        java.nio.file.attribute.AclEntryPermission.DELETE_CHILD,
        java.nio.file.attribute.AclEntryPermission.WRITE_ACL,
        java.nio.file.attribute.AclEntryPermission.WRITE_OWNER,
    )
    if (view.acl.isEmpty()) error("Windows user-home ACL is empty")
    val untrustedWritable = view.acl.any { entry ->
        entry.type() == java.nio.file.attribute.AclEntryType.ALLOW &&
            entry.permissions().intersect(writePermissions).isNotEmpty() &&
            !isTrustedWindowsParentPrincipal(entry.principal(), owner, system)
    }
    if (untrustedWritable) {
        error("Windows user home is writable by an untrusted principal")
    }
}

private fun isTrustedWindowsParentPrincipal(
    principal: UserPrincipal,
    owner: UserPrincipal,
    system: UserPrincipal,
): Boolean {
    if (principal.name.equals(owner.name, ignoreCase = true) ||
        principal.name.equals(system.name, ignoreCase = true)
    ) return true
    val suffix = principal.name.substringAfterLast('\\')
    return suffix.equals("Administrators", ignoreCase = true) ||
        suffix.equals("Creator Owner", ignoreCase = true)
}

private fun verifyWindowsJavaLogParentForOperation(parent: java.nio.file.Path) {
    if (!System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) return
    val homePath = parent.parent ?: error("Windows local log parent has no user home")
    verifyWindowsJavaParentTrust(homePath)
    verifyActiveLogDirectory(parent)
    verifyWindowsJavaExactOwnerSystemAcl(parent, directory = true)
}

private fun verifyWindowsJavaExactOwnerSystemAcl(path: java.nio.file.Path, directory: Boolean) {
    if (!System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) return
    val view = Files.getFileAttributeView(path, java.nio.file.attribute.AclFileAttributeView::class.java)
        ?: error("Windows local log ACL attributes are unavailable")
    val system = path.fileSystem.userPrincipalLookupService.lookupPrincipalByName(localSystemAccountName)
    val expectedPrincipals = listOf(view.owner, system).distinctBy { it.name.lowercase() }
    val expectedAcl = expectedPrincipals.map { principal ->
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
    verifyAcl(view, view.owner, expectedAcl)
}

private fun clearActiveJvmLogFile(path: java.nio.file.Path) {
    val absolute = path.toAbsolutePath().normalize()
    val parent = absolute.parent ?: error("Local log path has no parent")
    verifyWindowsJavaLogParentForOperation(parent)
    verifyActiveLogDirectory(parent)
    // Capture the pathname's parent before opening its stream. The stream is
    // only trusted after its descriptor identity matches this snapshot.
    val parentBeforeOpen = activeLogDirectoryAttributes(parent)
    Files.newDirectoryStream(parent).use { rawParent ->
        val secureParent = rawParent as? SecureDirectoryStream<java.nio.file.Path>
            ?: return clearActiveJvmLogFilePathFallback(absolute, parent, parentBeforeOpen)
        val openedParent = secureDirectoryAttributes(secureParent)
        if (!sameActiveFile(parentBeforeOpen, openedParent)) {
            error("Local log directory changed while opening its secure stream")
        }
        clearActiveJvmLogFileDescriptorRelative(
            secureParent,
            absolute.fileName,
            absolute,
            openedParent,
        )
    }
}

private fun clearActiveJvmLogFileDescriptorRelative(
    parent: SecureDirectoryStream<java.nio.file.Path>,
    fileName: java.nio.file.Path,
    absolute: java.nio.file.Path,
    directoryBefore: BasicFileAttributes,
) {
    var expected = secureBasicAttributes(parent, fileName)
    if (expected == null) {
        try {
            parent.newByteChannel(
                fileName,
                setOf(StandardOpenOption.WRITE, StandardOpenOption.CREATE_NEW, LinkOption.NOFOLLOW_LINKS),
                *ownerOnlyFileAttributes(),
            ).use { }
        } catch (_: FileAlreadyExistsException) {
            // The active entry was created by another local producer.
        }
        expected = secureBasicAttributes(parent, fileName)
    }
    val stableExpected = expected ?: error("Local log file disappeared during creation")
    if (stableExpected.isSymbolicLink || !stableExpected.isRegularFile) {
        error("Local log path is not a real file")
    }
    val channel = parent.newByteChannel(
        fileName,
        setOf(StandardOpenOption.WRITE, LinkOption.NOFOLLOW_LINKS),
    ) as? FileChannel ?: error("Secure local log channel is not a file channel")
    channel.use {
        val opened = secureBasicAttributes(parent, fileName)
            ?: error("Local log path disappeared while opening")
        if (!sameActiveFile(stableExpected, opened)) {
            error("Local log path changed while opening")
        }
        secureActiveJvmLogEntry(parent, fileName, absolute, opened)
        verifyActiveLogEntry(parent, fileName, opened, "after securing permissions")
        it.truncate(0)
        verifyActiveLogEntry(parent, fileName, opened, "after truncate")
        it.force(true)
        verifyActiveLogEntry(parent, fileName, opened, "after sync")
        if (!sameActiveFile(directoryBefore, secureDirectoryAttributes(parent))) {
            error("Local log directory changed after clearing")
        }
    }
}

private fun clearActiveJvmLogFilePathFallback(
    absolute: java.nio.file.Path,
    parent: java.nio.file.Path,
    parentBeforeOpen: BasicFileAttributes,
) {
    val directoryBefore = activeLogDirectoryAttributes(parent)
    if (!sameActiveFile(parentBeforeOpen, directoryBefore)) {
        error("Local log directory changed before fallback clear")
    }
    if (!Files.exists(absolute, LinkOption.NOFOLLOW_LINKS)) {
        try {
            FileChannel.open(
                absolute,
                setOf(StandardOpenOption.WRITE, StandardOpenOption.CREATE_NEW, LinkOption.NOFOLLOW_LINKS),
                *ownerOnlyFileAttributesIfSupported(parent),
            ).use { }
        } catch (_: FileAlreadyExistsException) {
            // The active entry was created by another local producer.
        }
    }
    val expected = activeLogFileAttributes(absolute)
    FileChannel.open(
        absolute,
        StandardOpenOption.WRITE,
        LinkOption.NOFOLLOW_LINKS,
    ).use { channel ->
        val directoryAfterOpen = activeLogDirectoryAttributes(parent)
        if (!sameActiveFile(directoryBefore, directoryAfterOpen)) {
            error("Local log directory changed while opening")
        }
        val opened = activeLogFileAttributes(absolute)
        if (!sameActiveFile(expected, opened)) {
            error("Local log path changed while opening")
        }
        secureActiveJvmLogEntry(absolute, opened)
        verifyActiveLogEntry(absolute, opened, "after securing permissions")
        channel.truncate(0)
        verifyActiveLogEntry(absolute, opened, "after truncate")
        channel.force(true)
        verifyActiveLogEntry(absolute, opened, "after sync")
        val directoryAfterSync = activeLogDirectoryAttributes(parent)
        if (!sameActiveFile(directoryBefore, directoryAfterSync)) {
            error("Local log directory changed after clearing")
        }
    }
}

private fun ownerOnlyFileAttributesIfSupported(path: java.nio.file.Path): Array<FileAttribute<*>> =
    if (Files.getFileAttributeView(path, PosixFileAttributeView::class.java) != null) {
        ownerOnlyFileAttributes()
    } else {
        emptyArray()
    }

private fun secureActiveJvmLogEntry(path: java.nio.file.Path, expected: BasicFileAttributes) {
    val posix = Files.getFileAttributeView(path, PosixFileAttributeView::class.java)
    val acl = Files.getFileAttributeView(path, java.nio.file.attribute.AclFileAttributeView::class.java)
    if (posix == null && acl == null) {
        error("Owner-only local log permissions are unavailable")
    }
    if (posix != null && posix.readAttributes().permissions() != ownerOnlyFilePermissions) {
        error("Local log file permissions are not owner-only")
    }
    if (acl != null) {
        restrictWithAclView(path, acl, directory = false)
    }
    if (!sameActiveFile(expected, activeLogFileAttributes(path))) {
        error("Local log path changed while verifying permissions")
    }
}

private fun secureActiveJvmLogEntry(
    parent: SecureDirectoryStream<java.nio.file.Path>,
    fileName: java.nio.file.Path,
    path: java.nio.file.Path,
    expected: BasicFileAttributes,
) {
    val posix = parent.getFileAttributeView(
        fileName,
        PosixFileAttributeView::class.java,
        LinkOption.NOFOLLOW_LINKS,
    )
    val acl = parent.getFileAttributeView(
        fileName,
        java.nio.file.attribute.AclFileAttributeView::class.java,
        LinkOption.NOFOLLOW_LINKS,
    )
    if (posix == null && acl == null) {
        error("Owner-only local log permissions are unavailable")
    }
    if (posix != null && posix.readAttributes().permissions() != ownerOnlyFilePermissions) {
        error("Local log file permissions are not owner-only")
    }
    if (acl != null) {
        restrictWithAclView(path, acl, directory = false)
    }
    val actual = secureBasicAttributes(parent, fileName)
        ?: error("Local log path disappeared while verifying permissions")
    if (!sameActiveFile(expected, actual)) {
        error("Local log path changed while verifying permissions")
    }
}

private fun verifyActiveLogEntry(
    parent: SecureDirectoryStream<java.nio.file.Path>,
    fileName: java.nio.file.Path,
    expected: BasicFileAttributes,
    stage: String,
) {
    if (!sameActiveFile(expected, secureBasicAttributes(parent, fileName) ?: error("Local log path disappeared $stage"))) {
        error("Local log path changed $stage")
    }
}

private fun activeLogDirectoryAttributes(path: java.nio.file.Path): BasicFileAttributes {
    verifyActiveLogDirectory(path)
    return Files.readAttributes(path, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
}

private fun activeLogFileAttributes(path: java.nio.file.Path): BasicFileAttributes {
    val attributes = Files.readAttributes(path, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    if (!attributes.isRegularFile || attributes.isSymbolicLink) {
        error("Local log path is not a real file")
    }
    return attributes
}

private fun sameActiveFile(left: BasicFileAttributes, right: BasicFileAttributes): Boolean {
    val leftKey = left.fileKey() ?: return false
    val rightKey = right.fileKey() ?: return false
    return leftKey == rightKey
}

private fun verifyActiveLogEntry(
    path: java.nio.file.Path,
    expected: BasicFileAttributes,
    stage: String,
) {
    if (!sameActiveFile(expected, activeLogFileAttributes(path))) {
        error("Local log path changed $stage")
    }
}

/**
 * Finds the fixed current entry without following aliases. Existing entries
 * must be readable regular files; unreadable entries fail closed.
 * Legacy-log migration and permission recovery are deliberately unsupported.
 */
internal fun ensureLogFileEntry(directory: java.nio.file.Path, logFile: java.nio.file.Path) {
    require(logFile.parent == directory) { "Local log storage must be a direct child" }
    verifyActiveLogDirectory(directory)
    val directoryBefore = activeLogDirectoryAttributes(directory)
    val entryExists = Files.newDirectoryStream(directory).use { entries ->
        entries.any { it.fileName == logFile.fileName }
    }
    if (!entryExists) {
        if (!sameActiveFile(directoryBefore, activeLogDirectoryAttributes(directory))) {
            error("Local log directory changed before file creation")
        }
        try {
            Files.createFile(logFile)
        } catch (_: FileAlreadyExistsException) {
            // A second local process may have created the same fixed file
            // after the directory snapshot. Validate and secure that entry.
        }
    }
    if (!sameActiveFile(directoryBefore, activeLogDirectoryAttributes(directory))) {
        error("Local log directory changed after file creation")
    }
    verifyRegularLogFile(logFile)
}

private fun verifyRegularLogFile(logFile: java.nio.file.Path) {
    val attributes = Files.readAttributes(logFile, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    if (!attributes.isRegularFile) error("Local log storage is not a regular file")
}

private fun restrictWithAcl(path: java.nio.file.Path, directory: Boolean) {
    val view = Files.getFileAttributeView(path, java.nio.file.attribute.AclFileAttributeView::class.java)
        ?: error("Owner-only file permissions are unavailable")
    restrictWithAclView(path, view, directory)
}

private fun restrictWithAclView(
    path: java.nio.file.Path,
    view: java.nio.file.attribute.AclFileAttributeView,
    directory: Boolean,
) {
    val lookup = path.fileSystem.userPrincipalLookupService
    val currentUser = if (System.getProperty("os.name").startsWith("Windows", ignoreCase = true)) {
        setWindowsOwner(path, currentWindowsUserSid())
        view.owner
    } else {
        view.owner
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
    Files.newByteChannel(
        path,
        StandardOpenOption.READ,
        StandardOpenOption.WRITE,
        LinkOption.NOFOLLOW_LINKS,
    ).use { }
}

private fun currentWindowsUserSid(): String {
    val systemRoot = windowsSystemRoot()
    val whoami = File(systemRoot, "System32/whoami.exe")
    if (!whoami.isFile) error("Windows identity tool is unavailable")
    // Successful helper output is internal identity metadata: parse it, then
    // erase the byte buffer. Failure output remains complete in the exception
    // so the owner-only application diagnostics can explain the failure.
    val result = runWindowsTool(ProcessBuilder(whoami.absolutePath, "/user", "/fo", "csv", "/nh"))
    val output = result.output
    if (result.timedOut) {
        val diagnostic = output.toString(Charsets.ISO_8859_1)
        output.fill(0)
        error("Windows identity lookup timed out: $diagnostic")
    }
    if (result.exitCode != 0 || output.size > maximumIdentityOutputBytes) {
        val diagnostic = output.toString(Charsets.ISO_8859_1)
        output.fill(0)
        error("Windows identity lookup failed: $diagnostic")
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
    val result = runWindowsTool(ProcessBuilder(
        powershell.absolutePath,
        "-NoLogo",
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        command,
    ))
    val output = result.output
    if (result.timedOut) {
        val diagnostic = output.toString(Charsets.UTF_8)
        output.fill(0)
        error("Windows identity translation timed out: $diagnostic")
    }
    if (result.exitCode != 0 || output.size > maximumIdentityOutputBytes) {
        val diagnostic = output.toString(Charsets.UTF_8)
        output.fill(0)
        error("Windows identity translation failed: $diagnostic")
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
    val result = runWindowsTool(ProcessBuilder(icacls.absolutePath, path.toString(), *arguments))
    val output = result.output
    if (result.timedOut) {
        val diagnostic = output.toString(Charsets.UTF_8)
        output.fill(0)
        error("Windows ACL protection timed out: $diagnostic")
    }
    if (result.exitCode != 0) {
        val diagnostic = output.toString(Charsets.UTF_8)
        output.fill(0)
        error("Windows ACL protection failed: $diagnostic")
    }
    output.fill(0)
}

private data class WindowsToolResult(
    val exitCode: Int,
    val output: ByteArray,
    val timedOut: Boolean,
)

private fun runWindowsTool(builder: ProcessBuilder): WindowsToolResult {
    val process = builder.redirectErrorStream(true).start()
    val output = ByteArrayOutputStream()
    val drained = CountDownLatch(1)
    thread(start = true, isDaemon = true, name = "dobby-windows-tool-output") {
        try {
            process.inputStream.use { it.copyTo(output) }
        } finally {
            drained.countDown()
        }
    }
    val finished = process.waitFor(aclProtectionTimeoutSeconds, TimeUnit.SECONDS)
    if (!finished) {
        process.destroyForcibly()
        if (!process.waitFor(aclProtectionTerminationSeconds, TimeUnit.SECONDS)) {
            error("Windows helper process did not terminate")
        }
    }
	if (!drained.await(aclProtectionTerminationSeconds, TimeUnit.SECONDS)) {
		// Preserve the complete diagnostic prefix collected before the bounded
		// drain deadline. Successful helper metadata is still erased by callers;
		// this branch is a failure and must retain its output in the exception so
		// owner-only application diagnostics can explain the incomplete drain.
		val diagnosticBytes = output.toByteArray()
		val diagnostic = diagnosticBytes.toString(Charsets.ISO_8859_1)
		diagnosticBytes.fill(0)
		output.reset()
		error("Windows helper output drain did not complete: $diagnostic")
	}
    return WindowsToolResult(
        exitCode = if (finished) process.exitValue() else -1,
        output = output.toByteArray(),
        timedOut = !finished,
    )
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
