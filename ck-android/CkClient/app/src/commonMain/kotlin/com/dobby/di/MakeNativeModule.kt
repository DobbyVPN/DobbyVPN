import com.dobby.feature.diagnostic.domain.IpRepository
import com.dobby.feature.logging.domain.CopyLogsInteractor
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.main.domain.AwgManager
import com.dobby.feature.main.domain.VpnManager
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import org.koin.core.module.Module
import org.koin.core.scope.Scope
import org.koin.dsl.module

typealias NativeInjectionFactory<T> = Scope.() -> T

fun makeNativeModule(
    copyLogsInteractor: NativeInjectionFactory<CopyLogsInteractor>,
    ipRepository: NativeInjectionFactory<IpRepository>,
    configsRepository: NativeInjectionFactory<DobbyConfigsRepository>,
    connectionStateRepository: NativeInjectionFactory<ConnectionStateRepository>,
    vpnManager: NativeInjectionFactory<VpnManager>,
    awgManager: NativeInjectionFactory<AwgManager>,
): Module {
    return module {
        factory { vpnManager() }
        factory { awgManager() }
        single { copyLogsInteractor() }
        single { ipRepository() }
        single { connectionStateRepository() }
        single { configsRepository() }
    }
}
