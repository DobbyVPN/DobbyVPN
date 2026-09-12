import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.dobby.di.initDesktopServiceLogger
import com.dobby.di.startDI
import com.dobby.navigation.App
import com.dobby.ui.theme.DesktopClientTheme

fun main()  {
    startDI(listOf(jvmMainModule, jvmVpnModule)) {}
    val serviceLoggerReady = initDesktopServiceLogger()
    application {
        // Launch the main window and call your shared App composable.
        Window(onCloseRequest = ::exitApplication, title = "Dobby VPN") {
            DesktopClientTheme {
                if (serviceLoggerReady) {
                    App()
                } else {
                    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Text("Local VPN diagnostics could not be initialized. Restart DobbyVPN and try again.")
                    }
                }
            }
        }
    }
}
