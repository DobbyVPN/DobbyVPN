import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.presentation.MainViewModel
import org.koin.androidx.viewmodel.dsl.viewModelOf
import org.koin.dsl.module

actual val sharedModule = module {
    single { PermissionEventsChannel() }
    viewModelOf(::LogsViewModel)
    viewModelOf(::MainViewModel)
}
