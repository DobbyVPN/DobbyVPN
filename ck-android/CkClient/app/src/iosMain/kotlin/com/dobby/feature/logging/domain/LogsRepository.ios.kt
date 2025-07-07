package com.dobby.feature.logging.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import platform.Foundation.NSFileManager

actual val fileSystem: FileSystem = FileSystem.SYSTEM

private const val appGroupIdentifier = "group.vpn.dobby.app"

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
    return logFilePath
}
