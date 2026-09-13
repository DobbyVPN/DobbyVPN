import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.dobby.feature.logging.LoggerManagerImpl
import com.dobby.navigation.App
import com.dobby.ui.theme.DesktopClientTheme
import interop.GrpcVpnLibrary

fun main()  {
    val dependencies = createDesktopAppDependencies()
    val serviceLoggerReady = LoggerManagerImpl(
        dependencies.logger,
        GrpcVpnLibrary.loggerGrpcLibrary,
    ).initLogger()
    application {
        // Launch the main window and call your shared App composable.
        Window(
            onCloseRequest = ::exitApplication,
            title = "Dobby VPN",
            onPreviewKeyEvent = { event ->
                if (event.type == KeyEventType.KeyDown && event.key == Key.Escape) {
                    dependencies.navigation.goBack()
                } else {
                    false
                }
            },
        ) {
            DesktopClientTheme {
                if (serviceLoggerReady) {
                    App(dependencies)
                } else {
                    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Text("Local VPN diagnostics could not be initialized. Restart DobbyVPN and try again.")
                    }
                }
            }
        }
    }
}
