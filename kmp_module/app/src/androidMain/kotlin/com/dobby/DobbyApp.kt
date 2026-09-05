package com.dobby

import android.app.Application
import com.dobby.di.startDI
import androidMainModule
import androidVpnModule
import com.dobby.feature.logging.domain.initLogFilePath
import com.dobby.feature.logging.domain.initLogger
import org.koin.android.ext.koin.androidContext

class DobbyApp : Application() {

    override fun onCreate() {
        super.onCreate()
        initLogFilePath(applicationContext)
        check(initLogger()) { "Go logger initialization returned false" }
        startDI(listOf(androidMainModule, androidVpnModule)) {
            androidContext(applicationContext)
        }
    }
}
