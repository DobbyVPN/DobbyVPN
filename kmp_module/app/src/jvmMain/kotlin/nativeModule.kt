import com.dobby.di.makeNativeModule
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.authentication.domain.AuthenticationManagerImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.GrpcSessionController
import com.dobby.feature.main.domain.SessionController
import interop.logger.LoggerLibrary
import interop.GrpcVpnLibrary
import interop.session.SessionLibrary
import com.dobby.feature.vpn_service.grpc.RestartableLoggerGrpcLibrary
import org.koin.dsl.module

val jvmMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl() },
    logEventsChannel = { LogEventsChannel() },
    logsRepository = { LogsRepository(logEventsChannel = get()) },
    configsRepository = {
        DobbyConfigsRepositoryImpl(
            healthCheckLibrary = get()
        )
    },
    connectionStateRepository = { ConnectionStateRepository() },
    authenticationManager = { AuthenticationManagerImpl() },
    loggerManager = { LoggerManagerImpl(get(), get()) },
)

val jvmVpnModule = module {
    single<SessionLibrary> { GrpcVpnLibrary.sessionGrpcLibrary }
    single<SessionController> { GrpcSessionController(get()) }
    single<LoggerLibrary> { RestartableLoggerGrpcLibrary(get()) }
}
