import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.IpRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.RestartableGRPCVPNLibrary
import interop.VPNLibrary
import org.koin.dsl.module

val jvmMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl() },
    logsRepository = { LogsRepository() },
    ipRepository = { IpRepositoryImpl(get()) },
    configsRepository = { DobbyConfigsRepositoryImpl( vpnLibrary = get() ) },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl(get()) },
    healthCheck = { HealthCheckImpl(get(), get()) }
)

val jvmVpnModule = module {
    single<VPNLibrary> { RestartableGRPCVPNLibrary(get()) }
    single<DobbyVpnService> { DobbyVpnService(get(), get(), get(), get()) }
}
