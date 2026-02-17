import com.dobby.feature.diagnostic.domain.HealthCheck
import com.dobby.feature.diagnostic.domain.IpRepository
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.authentication.domain.AuthenticationManager
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.LogEventsChannel
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.DobbyConfigsRepositoryAwg
import com.dobby.feature.main.domain.DobbyConfigsRepositoryCloak
import com.dobby.feature.main.domain.DobbyConfigsRepositoryOutline
import com.dobby.feature.main.domain.VpnManager
import org.koin.core.module.Module
import org.koin.core.scope.Scope
import org.koin.dsl.module

typealias NativeInjectionFactory<T> = Scope.() -> T

fun makeNativeModule(
    copyLogsInteractor: NativeInjectionFactory<CopyLogsInteractor>,
    logEventsChannel: NativeInjectionFactory<LogEventsChannel>,
    logsRepository: NativeInjectionFactory<LogsRepository>,
    ipRepository: NativeInjectionFactory<IpRepository>,
    configsRepository: NativeInjectionFactory<DobbyConfigsRepository>,
    connectionStateRepository: NativeInjectionFactory<ConnectionStateRepository>,
    vpnManager: NativeInjectionFactory<VpnManager>,
    authenticationManager: NativeInjectionFactory<AuthenticationManager>,
    healthCheck: NativeInjectionFactory<HealthCheck>,
): Module {
    return module {
        factory { vpnManager() }
        single { copyLogsInteractor() }
        single { logEventsChannel() }
        single { logsRepository() }
        single { Logger(get()) }
        single { ipRepository() }
        single { connectionStateRepository() }
        single { configsRepository() }
        single { authenticationManager() }

        single<DobbyConfigsRepositoryOutline> { get<DobbyConfigsRepository>() }
        single<DobbyConfigsRepositoryCloak> { get<DobbyConfigsRepository>() }
        single<DobbyConfigsRepositoryAwg> { get<DobbyConfigsRepository>() }
        single { healthCheck() }
    }
}
