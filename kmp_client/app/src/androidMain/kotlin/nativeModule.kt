import android.content.Context.MODE_PRIVATE
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.IpRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.AwgManagerImpl
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.feature.vpn_service.DobbyVpnInterfaceFactory
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.feature.vpn_service.domain.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.CloakLibFacadeImpl
import com.dobby.feature.vpn_service.domain.IpFetcher
import com.dobby.feature.vpn_service.domain.OutlineLibFacadeImpl
import org.koin.android.ext.koin.androidContext
import org.koin.core.module.dsl.factoryOf
import org.koin.dsl.module

val androidMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl(get()) },
    logsRepository = { LogsRepository() },
    ipRepository = { IpRepositoryImpl(get()) },
    configsRepository = {
        DobbyConfigsRepositoryImpl(
            prefs = androidContext().getSharedPreferences("DobbyPrefs", MODE_PRIVATE)
        )
    },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl(androidContext()) },
    awgManager = { AwgManagerImpl(androidContext()) },
    healthCheck = { HealthCheckImpl() }
)

val androidVpnModule = module {
    single { Logger(get()) }
    factoryOf(::IpFetcher)
    factory<CloakLibFacade> { CloakLibFacadeImpl() }
    factory<OutlineLibFacade> { OutlineLibFacadeImpl() }
    single<CloakConnectionInteractor> { CloakConnectionInteractor(get()) }
    factoryOf(::DobbyVpnInterfaceFactory)
}
