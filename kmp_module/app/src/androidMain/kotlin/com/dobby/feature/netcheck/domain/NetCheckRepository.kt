package com.dobby.feature.netcheck.domain

import android.content.Context
import okio.Path
import okio.Path.Companion.toPath

actual val fileSystem = okio.FileSystem.SYSTEM

private lateinit var appContext: Context

internal fun initNetCheckFilePath(context: Context) {
    appContext = context.applicationContext
}

actual fun provideNetCheckConfigPath(): Path {
    return "${appContext.filesDir.absolutePath}/net_check_config.yaml".toPath()
}
