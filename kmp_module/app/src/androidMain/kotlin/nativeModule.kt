import android.content.Context.MODE_PRIVATE
import com.dobby.di.makeNativeModule
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.authentication.domain.AuthenticationManagerImpl
import com.dobby.feature.diagnostic.domain.HealthCheckManagerImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DnsPreflightResolverImpl
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.feature.vpn_service.OutlineLibFacade
import com.dobby.feature.vpn_service.XrayLibFacade
import com.dobby.feature.vpn_service.domain.awg.AmneziaWGInteractor
import com.dobby.feature.vpn_service.TrustTunnelLibFacade

import com.dobby.feature.vpn_service.domain.trusttunnel.TrustTunnelLibFacadeImpl
import com.dobby.feature.vpn_service.domain.trusttunnel.TrustTunnelInteractor

import com.dobby.feature.vpn_service.domain.cloak.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.cloak.CloakLibFacadeImpl
import com.dobby.feature.vpn_service.domain.descriptor.FDManager
import com.dobby.feature.vpn_service.domain.georouting.GeoRouting
import com.dobby.feature.vpn_service.domain.outline.OutlineInteractor
import com.dobby.feature.vpn_service.domain.outline.OutlineLibFacadeImpl
import com.dobby.feature.vpn_service.domain.xray.XrayInteractor
import com.dobby.feature.vpn_service.domain.xray.XrayLibFacadeImpl
import org.koin.android.ext.koin.androidContext
import org.koin.dsl.module

val androidMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl(get()) },
    logEventsChannel = { LogEventsChannel() },
    logsRepository = { LogsRepository(logEventsChannel = get()) },
    configsRepository = {
        DobbyConfigsRepositoryImpl(
            prefs = androidContext().getSharedPreferences("DobbyPrefs", MODE_PRIVATE)
        )
    },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl(androidContext()) },
    authenticationManager = { AuthenticationManagerImpl(androidContext()) },
    healthCheckManager = { HealthCheckManagerImpl(get()) },
    loggerManager = { LoggerManagerImpl(get(), get()) },
    dnsPreflightResolver = { DnsPreflightResolverImpl(androidContext(), get()) }
)

val androidVpnModule = module {
    single { Logger(get()) }
    factory<CloakLibFacade> { CloakLibFacadeImpl() }
    factory<OutlineLibFacade> { OutlineLibFacadeImpl() }
    factory<XrayLibFacade> { XrayLibFacadeImpl() }
    factory<TrustTunnelLibFacade> { TrustTunnelLibFacadeImpl() }
    single<CloakConnectionInteractor> { CloakConnectionInteractor(get(), get(), get()) }
    single<OutlineInteractor> { OutlineInteractor(get(), get(), get(), get()) }
    single<AmneziaWGInteractor> { AmneziaWGInteractor(get(), get()) }
    single<XrayInteractor> { XrayInteractor(get(), get(), get(), get()) }
    single<TrustTunnelInteractor> { TrustTunnelInteractor(get(), get(), get(), get()) }
    single<GeoRouting> { GeoRouting( get() ) }
    single<FDManager> { FDManager() }
}
