import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.dobby.cli.runCliClient
import com.dobby.di.startDI
import com.dobby.navigation.App
import com.dobby.ui.theme.DesktopClientTheme


fun main(args: Array<String>)  {
    if (args.isNotEmpty()) {
        runCliClient(args)
    } else {
        application {
            startDI(listOf(jvmMainModule, jvmVpnModule)){}

            // Launch the main window and call your shared App composable.
            Window(onCloseRequest = ::exitApplication, title = "Dobby VPN") {
                DesktopClientTheme {
                    App()
                }
            }
        }
    }
}
