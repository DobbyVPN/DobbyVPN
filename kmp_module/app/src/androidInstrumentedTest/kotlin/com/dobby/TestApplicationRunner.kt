package com.dobby

import android.app.Application
import android.content.Context
import android.os.Bundle
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.runner.AndroidJUnitRunner
import com.dobby.AppDependenciesProvider
import com.dobby.backend.GoMobileBridge
import com.dobby.createAndroidAppDependencies
import com.dobby.feature.logging.domain.initLogFilePath
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.main.domain.SessionChangeEvents
import com.dobby.feature.vpn_service.DobbyVpnService

class TestApplication : Application(), AppDependenciesProvider {
    override lateinit var appDependencies: AppDependencies
        private set
    override lateinit var sessionChangeEvents: SessionChangeEvents
        private set

    override fun onCreate() {
        super.onCreate()
        // This Application belongs to the instrumentation APK; diagnostics and
        // the service must use the VPN application's private files directory.
        val targetContext = InstrumentationRegistry.getInstrumentation().targetContext
        initLogFilePath(targetContext)
        check(initLogger()) { "Go logger initialization returned false" }
        sessionChangeEvents = SessionChangeEvents()
        DobbyVpnService.nativePlatformRegistrar = if (TestRuntimeOptions.realProfileEnabled) {
            GoMobileBridge::registerSessionPlatform
        } else {
            {}
        }
        appDependencies = createAndroidAppDependencies(targetContext, sessionChangeEvents)
    }

    override fun onTerminate() {
        DobbyVpnService.resetNativePlatformRegistrar()
        super.onTerminate()
    }
}

class TestApplicationRunner : AndroidJUnitRunner() {
    private val appDependencies get() =
        (InstrumentationRegistry.getInstrumentation().targetContext.applicationContext as AppDependenciesProvider)
            .appDependencies

    override fun onCreate(arguments: Bundle?) {
        TestRuntimeOptions.realProfileEnabled = arguments?.getString(REAL_PROFILE_ARGUMENT) == "1"
        super.onCreate(arguments)
    }

    override fun newApplication(cl: ClassLoader, className: String, context: Context): Application =
        super.newApplication(cl, TestApplication::class.java.name, context)

    override fun onException(obj: Any?, error: Throwable): Boolean {
        try {
            appDependencies.logger.error(
                "[ERROR] instrumentation uncaught exception\n${error.stackTraceToString()}",
            )
        } catch (loggingFailure: Throwable) {
            error.addSuppressed(loggingFailure)
        }
        return super.onException(obj, error)
    }

    override fun finish(resultCode: Int, results: Bundle?) {
        TestRuntimeOptions.realProfileEnabled = false
        DobbyVpnService.resetNativePlatformRegistrar()
        super.finish(resultCode, results)
    }

    private companion object {
        const val REAL_PROFILE_ARGUMENT = "dobby.real_profile"
    }
}

private object TestRuntimeOptions {
    @Volatile var realProfileEnabled: Boolean = false
}
