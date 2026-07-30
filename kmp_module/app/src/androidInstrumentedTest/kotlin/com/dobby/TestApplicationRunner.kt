package com.dobby

import android.app.Application
import android.content.Context
import android.os.Bundle
import androidx.test.runner.AndroidJUnitRunner
import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.initLogFilePath
import com.dobby.feature.logging.domain.initLogger
import com.dobby.feature.logging.domain.provideGoLogFilePath
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import okio.Path.Companion.toPath
import org.koin.core.context.startKoin
import org.koin.core.context.stopKoin
import org.koin.dsl.module

class TestApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        initLogFilePath(applicationContext)
        if (TestRuntimeOptions.realProfileEnabled) {
            initLogger()
        }
        DobbyVpnService.nativePlatformRegistrar = if (TestRuntimeOptions.realProfileEnabled) {
            GoBackendWrapper::registerSessionPlatform
        } else {
            {}
        }
        startKoin {
            modules(module {
                single {
                    LogsRepository(
                        logFilePath = cacheDir.resolve("instrumentation.log").absolutePath.toPath(),
                        additionalLogFilePaths = listOf(provideGoLogFilePath()),
                    )
                }
                single { Logger(get()) }
                single { ConnectionStateRepository() }
            })
        }
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
