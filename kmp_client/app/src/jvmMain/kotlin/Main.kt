import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.dobby.di.startDI
import com.dobby.navigation.App
import com.dobby.ui.theme.DesktopClientTheme

fun main() = application {
    startDI(listOf(jvmMainModule, jvmVpnModule)){}

    // Launch the main window and call your shared App composable.
    Window(onCloseRequest = ::exitApplication, title = "Dobby VPN") {
        DesktopClientTheme {
            App()
        }
    }
}
