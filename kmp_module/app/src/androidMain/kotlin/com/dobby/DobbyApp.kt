package com.dobby

import android.app.Application
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.logging.domain.initLogFilePath

class DobbyApp : Application(), AppDependenciesProvider {
    override lateinit var appDependencies: AppDependencies
        private set

    override fun onCreate() {
        super.onCreate()
        initLogFilePath(applicationContext)
        check(initLogger()) { "Go logger initialization returned false" }
        appDependencies = createAndroidAppDependencies(applicationContext)
    }
}
