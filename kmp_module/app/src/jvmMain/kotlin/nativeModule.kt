import com.dobby.di.makeNativeModule
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.provideAdditionalLogFilePaths
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.GrpcSessionController
import com.dobby.feature.main.domain.SessionController
import interop.logger.LoggerLibrary
import interop.GrpcVpnLibrary
import interop.session.SessionLibrary
import org.koin.dsl.module

val jvmMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl() },
    logsRepository = {
        LogsRepository(additionalLogFilePaths = provideAdditionalLogFilePaths())
    },
    configsRepository = {
        DobbyConfigsRepositoryImpl()
    },
    connectionStateRepository = { ConnectionStateRepository() },
    loggerManager = { LoggerManagerImpl(get(), get()) },
)

val jvmVpnModule = module {
    single<SessionLibrary> { GrpcVpnLibrary.sessionGrpcLibrary }
    single<SessionController> { GrpcSessionController(get()) }
    single<LoggerLibrary> { GrpcVpnLibrary.loggerGrpcLibrary }
}
