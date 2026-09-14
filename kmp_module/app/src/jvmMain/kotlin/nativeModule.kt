import com.dobby.AppDependencies
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.provideAdditionalLogFilePaths
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.GrpcSessionController
import interop.GrpcVpnLibrary

fun createDesktopAppDependencies(): AppDependencies {
    val logsRepository = LogsRepository(additionalLogFilePaths = provideAdditionalLogFilePaths())
    return AppDependencies(
        permissionEventsChannel = PermissionEventsChannel(),
        sessionController = GrpcSessionController(GrpcVpnLibrary.sessionGrpcLibrary),
        logger = Logger(logsRepository),
        configsRepository = DobbyConfigsRepositoryImpl(),
        logsRepository = logsRepository,
        copyLogsInteractor = CopyLogsInteractorImpl(),
    )
}
