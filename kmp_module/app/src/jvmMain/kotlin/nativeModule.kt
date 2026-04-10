import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.authentication.domain.AuthenticationManagerImpl
import com.dobby.feature.diagnostic.IpRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.AwgManagerImpl
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.netcheck.NetCheckManagerImpl
import com.dobby.feature.netcheck.domain.NetCheckRepository
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.grpc.RestartableAwgGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableCloakGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableGeoroutingGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableHealthCheckGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableLoggerGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableNetCheckGrpcLibrary
import com.dobby.feature.vpn_service.grpc.RestartableOutlineGrpcLibrary
import interop.awg.AwgLibrary
import interop.cloak.CloakLibrary
import interop.georouting.GeoroutingLibrary
import interop.healthcheck.HealthCheckLibrary
import interop.logger.LoggerLibrary
import interop.netcheck.NetCheckLibrary
import interop.outline.OutlineLibrary
import org.koin.dsl.module

val jvmMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl() },
    logEventsChannel = { LogEventsChannel() },
    logsRepository = { LogsRepository(logEventsChannel = get()) },
    ipRepository = { IpRepositoryImpl(get()) },
    configsRepository = {
        DobbyConfigsRepositoryImpl(
            healthCheckLibrary = get()
        )
    },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl(get()) },
    awgManager = { AwgManagerImpl(get()) },
    authenticationManager = { AuthenticationManagerImpl() },
    healthCheck = {
        HealthCheckImpl(
            logger = get(),
            healthCheckLibrary = get()
        )
    },
    netCheckManager = { NetCheckManagerImpl(netCheckLibrary = get(), loggerLibrary = get()) },
    netCheckRepository = { NetCheckRepository() }
)

val jvmVpnModule = module {
    single<AwgLibrary> { RestartableAwgGrpcLibrary(get()) }
    single<OutlineLibrary> { RestartableOutlineGrpcLibrary(get()) }
    single<CloakLibrary> { RestartableCloakGrpcLibrary(get()) }
    single<HealthCheckLibrary> { RestartableHealthCheckGrpcLibrary(get()) }
    single<LoggerLibrary> { RestartableLoggerGrpcLibrary(get()) }
    single<GeoroutingLibrary> { RestartableGeoroutingGrpcLibrary(get()) }
    single<NetCheckLibrary> { RestartableNetCheckGrpcLibrary(get()) }
    single<DobbyVpnService> {
        DobbyVpnService(
            get(),
            logger = get(),
            awgLibrary = get(),
            outlineLibrary = get(),
            cloakLibrary = get(),
            loggerLibrary = get(),
            georoutingLibrary = get(),
            connectionState = get()
        )
    }
}
