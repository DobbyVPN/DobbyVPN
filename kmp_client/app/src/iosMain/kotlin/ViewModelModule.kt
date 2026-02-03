import com.dobby.feature.diagnostic.presentation.DiagnosticViewModel
import com.dobby.feature.authentication.presentation.AuthenticationSettingsViewModel
import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.presentation.MainViewModel
import com.dobby.feature.logging.presentation.SettingsViewModel
import org.koin.core.module.dsl.singleOf
import org.koin.dsl.module

actual val sharedModule = module {
    singleOf(::PermissionEventsChannel)
    singleOf(::LogsViewModel)
    singleOf(::DiagnosticViewModel)
    singleOf(::MainViewModel)
    singleOf(::AuthenticationSettingsViewModel)
    singleOf(::SettingsViewModel)
}
