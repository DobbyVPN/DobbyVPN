import android.content.Context.MODE_PRIVATE
import com.dobby.di.makeNativeModule
import com.dobby.domain.DobbyConfigsRepositoryImpl
import com.dobby.feature.logging.CopyLogsInteractorImpl
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.feature.logging.domain.LogsRepository
import com.dobby.feature.logging.domain.provideAdditionalLogFilePaths
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.AndroidSessionController
import com.dobby.feature.main.domain.SessionController
import org.koin.android.ext.koin.androidContext
import org.koin.dsl.module

val androidMainModule = makeNativeModule(
    copyLogsInteractor = { CopyLogsInteractorImpl(get(), get()) },
    logsRepository = {
        LogsRepository(additionalLogFilePaths = provideAdditionalLogFilePaths())
    },
    configsRepository = {
        DobbyConfigsRepositoryImpl(
            prefs = androidContext().getSharedPreferences("DobbyPrefs", MODE_PRIVATE)
        )
    },
    connectionStateRepository = { ConnectionStateRepository() },
    loggerManager = { LoggerManagerImpl(get()) },
)

val androidVpnModule = module {
    single { Logger(get()) }
    single<SessionController> { AndroidSessionController(androidContext(), get()) }
}
