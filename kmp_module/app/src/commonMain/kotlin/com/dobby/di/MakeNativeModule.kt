package com.dobby.di

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.LoggerManager
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.*
import org.koin.core.module.Module
import org.koin.core.scope.Scope
import org.koin.dsl.module

typealias NativeInjectionFactory<T> = Scope.() -> T

fun makeNativeModule(
    copyLogsInteractor: NativeInjectionFactory<CopyLogsInteractor>,
    logsRepository: NativeInjectionFactory<LogsRepository>,
    configsRepository: NativeInjectionFactory<DobbyConfigsRepository>,
    connectionStateRepository: NativeInjectionFactory<ConnectionStateRepository>,
    loggerManager: NativeInjectionFactory<LoggerManager>,
): Module {
    return module {
        factory { loggerManager() }

        single { copyLogsInteractor() }
        single { logsRepository() }
        single { Logger(get()) }
        single { connectionStateRepository() }
        single { configsRepository() }
    }
}
