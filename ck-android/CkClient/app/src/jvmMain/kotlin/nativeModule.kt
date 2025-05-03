import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.LogsRepositoryImpl
import com.dobby.feature.main.domain.AwgManagerImpl
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.VpnManagerImpl
import org.koin.dsl.module

val jvmMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl() },
    logsRepository = { LogsRepositoryImpl() },
    configsRepository = { DobbyConfigsRepositoryImpl() },
    connectionStateRepository = { ConnectionStateRepository() },
    vpnManager = { VpnManagerImpl() },
    awgManager = { AwgManagerImpl() }
)

val jvmVpnModule = module {}
