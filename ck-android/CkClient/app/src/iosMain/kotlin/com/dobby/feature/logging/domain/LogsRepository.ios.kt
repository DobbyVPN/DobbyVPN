package com.dobby.feature.logging.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import platform.Foundation.*

actual val fileSystem: FileSystem = FileSystem.SYSTEM

private const val appGroupIdentifier = "group.vpn.dobby.app"

actual fun provideLogFilePath(): Path {
    val fileManager = NSFileManager.defaultManager
    val containerURL: NSURL? = fileManager.containerURLForSecurityApplicationGroupIdentifier(appGroupIdentifier)

    val path = containerURL?.path ?: error("Failed to get shared container URL for $appGroupIdentifier")
    return "$path/app_logs.txt".toPath()
}
