package com.dobby.feature.logging.domain

import android.content.Context
import okio.Path
import okio.Path.Companion.toPath
import com.dobby.outline.OutlineGo

actual val fileSystem = okio.FileSystem.SYSTEM

private lateinit var appContext: Context

internal fun initLogFilePath(context: Context) {
    appContext = context.applicationContext
}

actual fun provideLogFilePath(): Path {
    return "${appContext.filesDir.absolutePath}/app_logs.txt".toPath()
}

fun initLogger() {
    OutlineGo.initLogger(provideLogFilePath().toString())
}