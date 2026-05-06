import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.dobby.cli.CliClient
import com.dobby.cli.ExitCode
import com.dobby.di.startDI
import com.dobby.navigation.App
import com.dobby.ui.theme.DesktopClientTheme
import kotlin.system.exitProcess

fun printHelp(exitCode: ExitCode) {
    println("""
dobby - CLI tool for managing connections, logs, and status

USAGE:
    ./dobby <command> [options]

COMMANDS:
    --help
        Show this help message

    logs
        Manage and view logs

        USAGE:
            ./dobby logs -n [N]
                Show last N log entries

            ./dobby logs clear
                Clear all logs

    connect
        Establish a connection using a configuration file

        USAGE:
            ./dobby connect <config_path> [--skip-healthcheck]

        ARGS:
            <config_path>
                Path to configuration file. Can be remote file provided via URL.

        OPTIONS:
            --skip-healthcheck
                Skip healthcheck confirmation after connecting

    disconnect
        Disconnect the current session

        USAGE:
            ./dobby disconnect

    status
        Show current system/connection status

        USAGE:
            ./dobby status [--json]

        OPTIONS:
            --json
                Output result in JSON format
                If not provided, output is printed in human-readable format
""".trimIndent())

    exitProcess(exitCode.value)
}

fun main(args: Array<String>)  {
    if (args.isNotEmpty()) {
        val cliClient = CliClient()
        val options = args.slice(1..args.lastIndex)
        when (args[0]) {
            "--help" -> printHelp(ExitCode.OK)
            "logs" -> {
                val exitCode = cliClient.logs(options)
                exitProcess(exitCode.value)
            }
            "connect" -> {
                val exitCode = cliClient.connect(options)
                exitProcess(exitCode.value)
            }
            "disconnect" -> {
                val exitCode = cliClient.disconnect(options)
                exitProcess(exitCode.value)
            }
            "status" -> {
                val exitCode = cliClient.status(options)
                exitProcess(exitCode.value)
            }
            else -> printHelp(ExitCode.INVALID_ARGS)
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
