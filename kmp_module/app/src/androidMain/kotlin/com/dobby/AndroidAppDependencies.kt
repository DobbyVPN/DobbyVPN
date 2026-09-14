package com.dobby

import android.content.Context
import android.content.Context.MODE_PRIVATE
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.provideAdditionalLogFilePaths
import com.dobby.feature.main.domain.AndroidSessionController
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.SessionChangeEvents

/** The Android activity and VPN service share this process-owned dependency graph. */
interface AppDependenciesProvider {
    val appDependencies: AppDependencies
    val sessionChangeEvents: SessionChangeEvents
}

fun createAndroidAppDependencies(context: Context, sessionChangeEvents: SessionChangeEvents): AppDependencies {
    val appContext = context.applicationContext
    val logsRepository = LogsRepository(additionalLogFilePaths = provideAdditionalLogFilePaths())
    val logger = Logger(logsRepository)
    return AppDependencies(
        permissionEventsChannel = PermissionEventsChannel(),
        sessionController = AndroidSessionController(appContext, sessionChangeEvents),
        logger = logger,
        configsRepository = DobbyConfigsRepositoryImpl(
            prefs = appContext.getSharedPreferences("DobbyPrefs", MODE_PRIVATE),
        ),
        logsRepository = logsRepository,
        copyLogsInteractor = CopyLogsInteractorImpl(appContext, logger),
    )
}
