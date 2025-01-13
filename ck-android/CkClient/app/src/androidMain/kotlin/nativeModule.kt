import android.content.Context.MODE_PRIVATE
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.LogsRepositoryImpl
import com.dobby.feature.main.domain.AwgManagerImpl
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import com.dobby.feature.vpn_service.CloakLibFacade
import com.dobby.feature.vpn_service.domain.CloakConnectionInteractor
import com.dobby.feature.vpn_service.domain.CloakLibFacadeImpl
import org.koin.android.ext.koin.androidContext
import org.koin.dsl.module

val androidMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl(get()) },
    logsRepository = { LogsRepositoryImpl({ androidContext().filesDir }) },
    configsRepository = {
        DobbyConfigsRepositoryImpl(
            prefs = androidContext().getSharedPreferences("DobbyPrefs", MODE_PRIVATE)
        )
    },
    connectionStateRepository = { ConnectionStateRepository },
    vpnManager = { VpnManagerImpl(androidContext()) },
    awgManager = { AwgManagerImpl(androidContext()) }
)

val androidVpnModule = module {
    factory<CloakLibFacade> { CloakLibFacadeImpl() }
    single<CloakConnectionInteractor> { CloakConnectionInteractor(get()) }
}
