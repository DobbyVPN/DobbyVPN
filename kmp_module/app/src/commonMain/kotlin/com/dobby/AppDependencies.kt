package com.dobby

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.ExportLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.navigation.AppNavigationState

/** Process-owned services shared by the UI and platform VPN shell. */
class AppDependencies(
    val permissionEventsChannel: PermissionEventsChannel,
    val sessionController: SessionController,
    val logger: Logger,
    private val configsRepository: DobbyConfigsRepository,
    private val logsRepository: LogsRepository,
    private val exportLogsInteractor: ExportLogsInteractor,
) {
    val navigation = AppNavigationState()

    /** UI ViewModels are created by Compose and live as long as its ViewModelStore. */
    fun createMainViewModel() = MainViewModel(
        configsRepository = configsRepository,
        permissionEventsChannel = permissionEventsChannel,
        sessionController = sessionController,
        logger = logger,
    )

    fun createLogsViewModel() = LogsViewModel(
        logsRepository = logsRepository,
        exportLogsInteractor = exportLogsInteractor,
    )
}
