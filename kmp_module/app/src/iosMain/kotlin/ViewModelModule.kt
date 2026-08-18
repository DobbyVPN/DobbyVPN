import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.IosSessionBridgeRegistry
import com.dobby.feature.main.domain.IosSessionController
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.presentation.MainViewModel
import org.koin.core.module.dsl.singleOf
import org.koin.dsl.module

actual val sharedModule = module {
    single<SessionController> {
        IosSessionController(IosSessionBridgeRegistry.requireBridge(), get())
    }
    singleOf(::PermissionEventsChannel)
    singleOf(::LogsViewModel)
    singleOf(::MainViewModel)
}
