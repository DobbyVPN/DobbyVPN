package com.dobby.feature.logging.domain

import kotlinx.cinterop.ExperimentalForeignApi
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
import platform.Foundation.NSTemporaryDirectory

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
private var logStorageInitializationAvailable = true

actual fun platformLogStorageInitializationAvailable(): Boolean = logStorageInitializationAvailable

actual fun clearLogFile(path: Path, storageFileSystem: FileSystem) {
    storageFileSystem.sink(path).buffer().use { }
}

@OptIn(ExperimentalForeignApi::class)
actual fun provideLogFilePath(): Path = initializedLogPath(
    if (isTunnelProcess()) "tunnel_logs.jsonl" else "app_logs.txt",
)

@OptIn(ExperimentalForeignApi::class)
actual fun provideGoLogFilePath(): Path = initializedLogPath(
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
    val containerPath = containerURL?.path ?: run {
        IllegalStateException("iOS App Group log container is unavailable").printStackTrace()
        logStorageInitializationAvailable = false
        NSTemporaryDirectory().trimEnd('/')
    }
    return "$containerPath/$name".toPath()
}

@OptIn(ExperimentalForeignApi::class)
private fun initializedLogPath(name: String): Path {
    val logFilePath = sharedLogPath(name)
    try {
        fileSystem.appendingSink(logFilePath).use { }
    } catch (failure: Throwable) {
        failure.printStackTrace()
        logStorageInitializationAvailable = false
    }
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
        "process=${processInfo.processName}"
}
