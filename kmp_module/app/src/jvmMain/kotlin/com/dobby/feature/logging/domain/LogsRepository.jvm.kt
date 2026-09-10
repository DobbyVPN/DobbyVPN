package com.dobby.feature.logging.domain

import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.BasicFileAttributes
import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import okio.buffer
import okio.use

actual val fileSystem: FileSystem = FileSystem.SYSTEM
private val logWriteLock = Any()

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
    val file = java.nio.file.Path.of(path.toString())
    verifyRegularLogFile(file)
    Files.newByteChannel(
        file,
        StandardOpenOption.WRITE,
        StandardOpenOption.TRUNCATE_EXISTING,
        LinkOption.NOFOLLOW_LINKS,
    ).use { }
}

private fun provideLogFile(name: String): Path {
    val userHome = System.getProperty("user.home") ?: error("Unable to get user home directory")
    val directory = java.nio.file.Path.of(userHome, ".dobbyvpn")
    val logFile = directory.resolve(name)
    Files.createDirectories(directory)
    Files.newByteChannel(
        logFile,
        StandardOpenOption.CREATE,
        StandardOpenOption.WRITE,
        StandardOpenOption.APPEND,
    ).use { }
    return logFile.toString().toPath()
}

private fun verifyRegularLogFile(path: java.nio.file.Path) {
    val attributes = Files.readAttributes(path, BasicFileAttributes::class.java, LinkOption.NOFOLLOW_LINKS)
    if (!attributes.isRegularFile || attributes.isSymbolicLink) error("Local log storage is not a regular file")
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
