import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.dobby.cli.CliClient
import com.dobby.di.startDI
import com.dobby.navigation.App
import com.dobby.ui.theme.DesktopClientTheme
import kotlin.system.exitProcess

fun printHelp(statusCode: Int) {
    println("""
Usage:
  dobby --terminal <config_path>
  dobby --help
  dobby

Description:
  DobbyVPN client with CLI run support

Arguments:
  <config_path>   Path to configuration file.

Options:
  --terminal      Run CLI client
  --help          Show this help message and exit

Examples:
  dobby --terminal /path/to/config.toml
  dobby --help
  dobby

Errors:
   - Configuration file not found or not readable
""".trimIndent())

    exitProcess(statusCode)
}

fun main(args: Array<String>)  {
    if (args.isNotEmpty()) {
        if (args.size == 2 && args[0] == "--terminal") {
            val cliClient = CliClient(args[1])
            cliClient.runClient()
        } else {
            printHelp(1)
        }
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
