import com.dobby.feature.authentication.presentation.AuthenticationSettingsViewModel
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.logging.presentation.SettingsViewModel
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.IosSessionBridgeRegistry
import com.dobby.feature.main.domain.IosSessionController
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.presentation.ConfigsProcessor
import com.dobby.feature.main.presentation.MainViewModel
import org.koin.core.module.dsl.singleOf
import org.koin.dsl.module

actual val sharedModule = module {
    single<SessionController> { IosSessionController(IosSessionBridgeRegistry.requireBridge()) }
    singleOf(::PermissionEventsChannel)
    singleOf(::LogsViewModel)
    singleOf(::ConfigsProcessor)
    singleOf(::MainViewModel)
    singleOf(::AuthenticationSettingsViewModel)
    singleOf(::SettingsViewModel)
}
