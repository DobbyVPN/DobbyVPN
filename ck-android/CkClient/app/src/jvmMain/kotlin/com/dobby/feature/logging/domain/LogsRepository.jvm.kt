package com.dobby.feature.logging.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import java.io.File

actual val fileSystem: FileSystem = FileSystem.SYSTEM

actual fun provideLogFilePath(): Path {
    val userHome = System.getProperty("user.home") ?: error("Unable to get user home directory")
    val appDir = File(userHome, ".myapp")
    if (!appDir.exists()) {
        appDir.mkdirs()
    }
    val logFile = File(appDir, "app_logs.txt")
    return logFile.absolutePath.toPath()
}