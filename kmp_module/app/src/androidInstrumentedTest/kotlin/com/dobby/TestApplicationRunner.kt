package com.dobby

import android.app.Application
import android.content.Context
import android.os.Bundle
import androidx.test.runner.AndroidJUnitRunner
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import okio.Path.Companion.toPath
import org.koin.core.context.startKoin
import org.koin.core.context.stopKoin
import org.koin.dsl.module

class TestApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        DobbyVpnService.nativePlatformRegistrar = {}
        startKoin {
            modules(module {
                single { LogEventsChannel() }
                single {
                    LogsRepository(
                        logFilePath = cacheDir.resolve("instrumentation.log").absolutePath.toPath(),
                        logEventsChannel = get(),
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
    override fun newApplication(cl: ClassLoader, className: String, context: Context): Application =
        super.newApplication(cl, TestApplication::class.java.name, context)

    override fun finish(resultCode: Int, results: Bundle?) {
        DobbyVpnService.resetNativePlatformRegistrar()
        stopKoin()
        super.finish(resultCode, results)
    }
}
