package com.dobby.feature.netcheck.domain

import okio.FileSystem
import okio.Path
import okio.Path.Companion.toPath
import java.io.File


actual val fileSystem: FileSystem = FileSystem.SYSTEM

actual fun provideNetCheckConfigPath(): Path {
    val userHome = System.getProperty("user.home") ?: error("Unable to get user home directory")
    val appDir = File(userHome, ".myapp")
    if (!appDir.exists()) {
        appDir.mkdirs()
    }
    val logFile = File(appDir, "net_check_config.yaml")
    return logFile.absolutePath.toPath()
}
