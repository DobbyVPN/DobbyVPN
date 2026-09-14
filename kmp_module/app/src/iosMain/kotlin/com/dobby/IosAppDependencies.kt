package com.dobby

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.ExportLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.IosSessionBridge
import com.dobby.feature.main.domain.IosSessionController
import com.dobby.feature.main.domain.PermissionEventsChannel

/** Builds the app graph at the Swift entrypoint, where the provider bridge is available. */
fun createIosAppDependencies(
    bridge: IosSessionBridge,
    logsRepository: LogsRepository,
    exportLogsInteractor: ExportLogsInteractor,
    configsRepository: DobbyConfigsRepository,
): AppDependencies {
    return AppDependencies(
        permissionEventsChannel = PermissionEventsChannel(),
        sessionController = IosSessionController(bridge),
        logger = Logger(logsRepository),
        configsRepository = configsRepository,
        logsRepository = logsRepository,
        exportLogsInteractor = exportLogsInteractor,
    )
}
