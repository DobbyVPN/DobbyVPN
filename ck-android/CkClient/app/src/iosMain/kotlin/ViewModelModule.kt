import com.dobby.feature.logging.presentation.LogsViewModel
import com.dobby.feature.diagnostic.presentation.DiagnosticViewModel
import com.dobby.feature.main.presentation.MainViewModel
import org.koin.core.module.dsl.singleOf
import org.koin.dsl.module

actual val sharedModule = module {
    singleOf(::LogsViewModel)
    singleOf(::DiagnosticViewModel)
    singleOf(::MainViewModel)
}
