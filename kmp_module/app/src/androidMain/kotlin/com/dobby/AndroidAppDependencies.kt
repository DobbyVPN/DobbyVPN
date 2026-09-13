package com.dobby

import android.content.Context
import android.content.Context.MODE_PRIVATE
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.provideAdditionalLogFilePaths
import com.dobby.feature.main.domain.AndroidSessionController
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.PermissionEventsChannel

/** The Android activity and VPN service share this process-owned dependency graph. */
interface AppDependenciesProvider {
    val appDependencies: AppDependencies
}

fun createAndroidAppDependencies(context: Context): AppDependencies {
    val appContext = context.applicationContext
    val connectionStateRepository = ConnectionStateRepository()
    val logsRepository = LogsRepository(additionalLogFilePaths = provideAdditionalLogFilePaths())
    val logger = Logger(logsRepository)
    return AppDependencies(
        connectionStateRepository = connectionStateRepository,
        permissionEventsChannel = PermissionEventsChannel(),
        sessionController = AndroidSessionController(appContext, connectionStateRepository),
        logger = logger,
        configsRepository = DobbyConfigsRepositoryImpl(
            prefs = appContext.getSharedPreferences("DobbyPrefs", MODE_PRIVATE),
        ),
        logsRepository = logsRepository,
        copyLogsInteractor = CopyLogsInteractorImpl(appContext, logger),
    )
}
