import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.diagnostic.IpRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.main.domain.AwgManagerImpl
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.DobbyVpnService
import interop.VPNLibraryLoader
import org.koin.dsl.module

val jvmMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl() },
    logsRepository = { LogsRepository() },
    ipRepository = { IpRepositoryImpl(get()) },
    configsRepository = { DobbyConfigsRepositoryImpl( vpnLibrary = get() ) },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl(get()) },
    awgManager = { AwgManagerImpl(get()) },
    healthCheck = { HealthCheckImpl() }
)

val jvmVpnModule = module {
    single<VPNLibraryLoader> { VPNLibraryLoader(get()) }
    single<DobbyVpnService> { DobbyVpnService(get(), get(), get(), get()) }
}
