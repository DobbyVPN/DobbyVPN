package com.dobby

import android.app.Application
import com.dobby.di.startDI
import androidMainModule
import androidVpnModule
import com.dobby.feature.logging.domain.initLogFilePath
import org.koin.android.ext.koin.androidContext

class DobbyApp : Application() {

    override fun onCreate() {
        super.onCreate()
        initLogFilePath(applicationContext)
        startDI(listOf(androidMainModule, androidVpnModule)) {
            androidContext(applicationContext)
        }
    }
}
