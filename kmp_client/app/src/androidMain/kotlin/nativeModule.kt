import android.content.Context.MODE_PRIVATE
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.diagnostic.IpRepositoryImpl
import com.dobby.feature.diagnostic.domain.HealthCheckImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.authentication.domain.AuthenticationManagerImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.main.domain.AwgManagerImpl
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.feature.vpn_service.DobbyVpnInterfaceFactory
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.feature.vpn_service.XrayLibFacade
import com.dobby.feature.vpn_service.domain.XrayLibFacadeImpl

import com.dobby.feature.vpn_service.domain.cloak.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.cloak.CloakLibFacadeImpl
import com.dobby.feature.vpn_service.domain.outline.OutlineInteractor
import com.dobby.feature.vpn_service.domain.outline.OutlineLibFacadeImpl
import org.koin.android.ext.koin.androidContext
import org.koin.core.module.dsl.factoryOf
import org.koin.dsl.module

val androidMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl(get()) },
    logEventsChannel = { LogEventsChannel() },
    logsRepository = { LogsRepository( logEventsChannel = get()) },
    ipRepository = { IpRepositoryImpl(get()) },
    configsRepository = {
        DobbyConfigsRepositoryImpl(
            prefs = androidContext().getSharedPreferences("DobbyPrefs", MODE_PRIVATE)
        )
    },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl(androidContext()) },
    awgManager = { AwgManagerImpl(androidContext()) },
    authenticationManager = { AuthenticationManagerImpl(androidContext())},
    healthCheck = { HealthCheckImpl(get()) }
)

val androidVpnModule = module {
    single { Logger(get()) }
    factory<CloakLibFacade> { CloakLibFacadeImpl() }
    factory<OutlineLibFacade> { OutlineLibFacadeImpl() }
    factory<XrayLibFacade> { XrayLibFacadeImpl() }
    single<CloakConnectionInteractor> { CloakConnectionInteractor(get(), get(), get()) }
    single<OutlineInteractor> { OutlineInteractor(get(), get(), get()) }
    factoryOf(::DobbyVpnInterfaceFactory)
}
