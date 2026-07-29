package com.dobby.feature.logging.domain

import android.content.Context
import android.os.Build
import okio.Path
import okio.Path.Companion.toPath
import com.dobby.backend.GoBackendWrapper

actual val fileSystem = okio.FileSystem.SYSTEM

private lateinit var appContext: Context
private val logWriteLock = Any()

actual fun <T> withLogWriteLock(block: () -> T): T = synchronized(logWriteLock, block)

internal fun initLogFilePath(context: Context) {
    appContext = context.applicationContext
}

actual fun provideLogFilePath(): Path = "${appContext.filesDir.absolutePath}/app_logs.txt".toPath()

actual fun provideGoLogFilePath(): Path = "${appContext.filesDir.absolutePath}/go_android_logs.jsonl".toPath()

actual fun provideAdditionalLogFilePaths(): List<Path> = listOf(provideGoLogFilePath())

actual fun platformLogInfo(): String {
    return "platform=android " +
        "sdk=${Build.VERSION.SDK_INT} " +
        "release=${Build.VERSION.RELEASE} " +
        "codename=${Build.VERSION.CODENAME} " +
        "incremental=${Build.VERSION.INCREMENTAL} " +
        "manufacturer=${Build.MANUFACTURER} " +
        "brand=${Build.BRAND} " +
        "model=${Build.MODEL} " +
        "device=${Build.DEVICE} " +
        "product=${Build.PRODUCT} " +
        "hardware=${Build.HARDWARE} " +
        "abis=${Build.SUPPORTED_ABIS?.joinToString(",").orEmpty()}"
}

fun initLogger() {
    GoBackendWrapper.initLogger(provideGoLogFilePath().toString())
}
