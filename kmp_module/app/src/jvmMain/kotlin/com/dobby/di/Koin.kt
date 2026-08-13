package com.dobby.di

import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.logging.LoggerManager
import org.koin.mp.KoinPlatform

fun getMainViewModel(): MainViewModel = KoinPlatform.getKoin().get()

/** Starts the local Go-service log sink once the desktop dependency graph is available. */
fun initDesktopServiceLogger(): Boolean = try {
    KoinPlatform.getKoin().get<LoggerManager>().initLogger()
} catch (_: Exception) {
    false
}
