package com.dobby.feature.logging.domain

import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.convert
import kotlinx.cinterop.useContents
import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import platform.Foundation.NSFileManager
import platform.Foundation.NSProcessInfo
import platform.posix.chmod

actual val fileSystem: FileSystem = FileSystem.SYSTEM

private const val appGroupIdentifier = "group.vpn.dobby.app"

@OptIn(ExperimentalForeignApi::class)
actual fun provideLogFilePath(): Path {

    val fileManager = NSFileManager.defaultManager
    val containerURL = fileManager.containerURLForSecurityApplicationGroupIdentifier(appGroupIdentifier)

    val path = containerURL?.path ?: error("Failed to get shared container URL for $appGroupIdentifier")
    val logFilePath = "$path/app_logs.txt".toPath()

    if (!fileSystem.exists(logFilePath)) {
        fileSystem.createDirectories(logFilePath.parent!!)
        fileSystem.write(logFilePath) { writeUtf8("") }
        println("Log file created at: $logFilePath")
    } else {
        println("Log file already exists at: $logFilePath")
    }
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
