import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.IpRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.authentication.domain.AuthenticationManagerImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.RestartableGRPCVPNLibrary
import interop.VPNLibrary
import org.koin.dsl.module

val jvmMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl() },
    logEventsChannel = { LogEventsChannel() },
    logsRepository = { LogsRepository( logEventsChannel = get()) },
    ipRepository = { IpRepositoryImpl(get()) },
    configsRepository = { DobbyConfigsRepositoryImpl( vpnLibrary = get() ) },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl(get()) },
    authenticationManager = { AuthenticationManagerImpl() },
    healthCheck = { HealthCheckImpl(get(), get()) }
)

val jvmVpnModule = module {
    single<VPNLibrary> { RestartableGRPCVPNLibrary(get()) }
    single<DobbyVpnService> { DobbyVpnService(get(), get(), get(), get()) }
}
