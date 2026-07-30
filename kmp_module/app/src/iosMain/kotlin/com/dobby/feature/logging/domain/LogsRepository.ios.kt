package com.dobby.feature.logging.domain

import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.convert
import kotlinx.cinterop.useContents
import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import okio.buffer
import okio.use
import platform.Foundation.NSBundle
import platform.Foundation.NSFileManager
import platform.Foundation.NSProcessInfo
import platform.Foundation.NSRecursiveLock
import platform.posix.chmod

actual val fileSystem: FileSystem = FileSystem.SYSTEM
private val logWriteLock = NSRecursiveLock()

actual fun <T> withLogWriteLock(block: () -> T): T {
    logWriteLock.lock()
    return try {
        block()
    } finally {
        logWriteLock.unlock()
    }
}

private const val appGroupIdentifier = "group.vpn.dobby.app"

@OptIn(ExperimentalForeignApi::class)
actual fun provideLogFilePath(): Path = secureLogPath(
    if (isTunnelProcess()) "tunnel_logs.jsonl" else "app_logs.txt",
)

@OptIn(ExperimentalForeignApi::class)
actual fun provideGoLogFilePath(): Path = secureLogPath(
    if (isTunnelProcess()) "go_tunnel_logs.jsonl" else "go_app_logs.jsonl",
)

@OptIn(ExperimentalForeignApi::class)
actual fun provideAdditionalLogFilePaths(): List<Path> {
    val current = provideLogFilePath()
    return listOf("app_logs.txt", "tunnel_logs.jsonl", "go_app_logs.jsonl", "go_tunnel_logs.jsonl")
        .map(::sharedLogPath)
        .filterNot { it == current }
}

private fun isTunnelProcess(): Boolean = NSBundle.mainBundle.bundleIdentifier?.endsWith(".tunnel") == true

@OptIn(ExperimentalForeignApi::class)
private fun sharedLogPath(name: String): Path {
    val fileManager = NSFileManager.defaultManager
    val containerURL = fileManager.containerURLForSecurityApplicationGroupIdentifier(appGroupIdentifier)
    val containerPath = containerURL?.path ?: error("Failed to get shared log container")
    return "$containerPath/$name".toPath()
}

@OptIn(ExperimentalForeignApi::class)
private fun secureLogPath(name: String): Path {
    val logFilePath = sharedLogPath(name)
    fileSystem.createDirectories(logFilePath.parent!!)
    // appendingSink creates an absent file without truncating a file another
    // app-group process may have created between the existence check and open.
    fileSystem.appendingSink(logFilePath).buffer().use { }
    check(chmod(logFilePath.parent.toString(), 448.convert()) == 0) { "Failed to secure iOS log directory" }
    check(chmod(logFilePath.toString(), 384.convert()) == 0) { "Failed to secure iOS log file" }
    return logFilePath
}

@OptIn(ExperimentalForeignApi::class)
actual fun platformLogInfo(): String {
    val processInfo = NSProcessInfo.processInfo
    val version = processInfo.operatingSystemVersion.useContents {
        "$majorVersion.$minorVersion.$patchVersion"
    }
    return "platform=ios " +
        "osVersion=$version " +
        "osDescription=${processInfo.operatingSystemVersionString} " +
        "process=${processInfo.processName} " +
        "physicalMemory=${processInfo.physicalMemory}"
}
