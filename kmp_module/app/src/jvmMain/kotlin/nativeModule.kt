import com.dobby.di.makeNativeModule
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.authentication.domain.AuthenticationManagerImpl
import com.dobby.feature.diagnostic.domain.HealthCheckManagerImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.grpc.*
import interop.awg.AwgLibrary
import interop.cloak.CloakLibrary
import interop.georouting.GeoroutingLibrary
import interop.healthcheck.HealthCheckLibrary
import interop.logger.LoggerLibrary
import interop.outline.OutlineLibrary
import interop.xray.XrayLibrary
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
    vpnManager = { VpnManagerImpl(get(), get()) },
    authenticationManager = { AuthenticationManagerImpl() },
    healthCheckManager = { HealthCheckManagerImpl(get(), get()) },
    loggerManager = { LoggerManagerImpl(get(), get(), get()) }
)

val jvmVpnModule = module {
    single<AwgLibrary> { RestartableAwgGrpcLibrary(get()) }
    single<OutlineLibrary> { RestartableOutlineGrpcLibrary(get()) }
    single<XrayLibrary> { RestartableXrayGrpcLibrary(get()) }
    single<CloakLibrary> { RestartableCloakGrpcLibrary(get()) }
    single<HealthCheckLibrary> { RestartableHealthCheckGrpcLibrary(get()) }
    single<LoggerLibrary> { RestartableLoggerGrpcLibrary(get()) }
    single<GeoroutingLibrary> { RestartableGeoroutingGrpcLibrary(get()) }
    single<DobbyVpnService> {
        DobbyVpnService(
            get(),
            logger = get(),
            awgLibrary = get(),
            outlineLibrary = get(),
            xrayLibrary = get(),
            cloakLibrary = get(),
            georoutingLibrary = get()
        )
    }
}
