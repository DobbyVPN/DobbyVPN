package com.dobby.feature.netcheck.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import platform.Foundation.NSFileManager

actual val fileSystem: FileSystem = FileSystem.SYSTEM

private const val appGroupIdentifier = "group.vpn.dobby.app"

actual fun provideNetCheckConfigPath(): Path {
    val fileManager = NSFileManager.defaultManager
    val containerURL = fileManager.containerURLForSecurityApplicationGroupIdentifier(appGroupIdentifier)

    val path = containerURL?.path ?: error("Failed to get shared container URL for $appGroupIdentifier")
    val netCheckFilePath = "$path/net_check_config.yaml".toPath()

    if (!fileSystem.exists(netCheckFilePath)) {
        fileSystem.createDirectories(netCheckFilePath.parent!!)
        fileSystem.write(netCheckFilePath) { writeUtf8("") }
        println("Net check file created at: $netCheckFilePath")
    } else {
        println("Net check file already exists at: $netCheckFilePath")
    }
    return netCheckFilePath
}
