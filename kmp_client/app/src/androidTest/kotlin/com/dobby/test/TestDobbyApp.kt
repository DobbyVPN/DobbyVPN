package com.dobby.test

import android.app.Application
import com.dobby.feature.logging.domain.initLogFilePath

class TestDobbyApp : Application() {
    override fun onCreate() {
        super.onCreate()
        // Keep instrumented bootstrap minimal and avoid native logger init.
        initLogFilePath(applicationContext)
    }
}
