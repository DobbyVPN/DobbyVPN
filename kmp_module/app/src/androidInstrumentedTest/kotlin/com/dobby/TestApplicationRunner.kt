package com.dobby

import android.app.Application
import android.content.Context
import android.os.Bundle
import androidx.test.runner.AndroidJUnitRunner
import androidMainModule
import androidVpnModule
import com.dobby.backend.GoBackendWrapper
import com.dobby.di.startDI
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.initLogFilePath
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.vpn_service.DobbyVpnService
import org.koin.android.ext.koin.androidContext
import org.koin.core.context.GlobalContext
import org.koin.core.context.stopKoin

class TestApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        initLogFilePath(applicationContext)
        check(initLogger()) { "Go logger initialization returned false" }
        DobbyVpnService.nativePlatformRegistrar = if (TestRuntimeOptions.realProfileEnabled) {
            GoBackendWrapper::registerSessionPlatform
        } else {
            {}
        }
        // Consent uses the real application activity. Reuse its dependencies
        // instead of maintaining an incomplete parallel test application graph.
        startDI(listOf(androidMainModule, androidVpnModule)) {
            androidContext(applicationContext)
        }
        // Koin definitions are lazy. Resolve the logger once so the
        // target application creates its canonical files/app_logs.txt before
        // any hosted operation starts.
        GlobalContext.get().get<Logger>()
    }

    override fun onTerminate() {
        DobbyVpnService.resetNativePlatformRegistrar()
        super.onTerminate()
    }
}

class TestApplicationRunner : AndroidJUnitRunner() {
    override fun onCreate(arguments: Bundle?) {
        TestRuntimeOptions.realProfileEnabled = arguments?.getString(REAL_PROFILE_ARGUMENT) == "1"
        super.onCreate(arguments)
    }

    override fun newApplication(cl: ClassLoader, className: String, context: Context): Application =
        super.newApplication(cl, TestApplication::class.java.name, context)

    override fun onException(obj: Any?, error: Throwable): Boolean {
        try {
            GlobalContext.get().get<Logger>().error(
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
        stopKoin()
        super.finish(resultCode, results)
    }

    private companion object {
        const val REAL_PROFILE_ARGUMENT = "dobby.real_profile"
    }
}

private object TestRuntimeOptions {
    @Volatile var realProfileEnabled: Boolean = false
}
