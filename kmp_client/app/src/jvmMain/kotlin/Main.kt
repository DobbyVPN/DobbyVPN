import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.dobby.di.startDI
import com.dobby.feature.logging.Logger
import com.dobby.navigation.App
import com.dobby.ui.theme.DesktopClientTheme
import com.sun.jna.Platform
import org.koin.mp.KoinPlatform
import java.io.File
import java.io.IOException
import java.net.URLDecoder
import java.nio.charset.StandardCharsets

fun ensureAdminPrivilegesMacOS() {
    if (!isRunningAsRoot()) {
        try {
            val appPath = File(
                object {}.javaClass.protectionDomain.codeSource.location.toURI()
            ).parentFile.parentFile.parentFile.absolutePath

            val dobbyApp = File(appPath, "Contents/MacOS/Dobby\\ Vpn")

            val command = arrayOf("sudo", dobbyApp.absolutePath)

            println("Executing: ${command.joinToString(" ")}")

            val process = ProcessBuilder(*command)
                .redirectErrorStream(true)
                .inheritIO()
                .start()

            val exitCode = process.waitFor()
            println("Process exited with code $exitCode")
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }
}


fun isRunningAsRoot(): Boolean {
    return try {
        val process = ProcessBuilder("id", "-u").start()
        val output = process.inputStream.bufferedReader().readText().trim()
        output == "0"
    } catch (e: IOException) {
        false
    }
}

fun main() = application {
    if (Platform.isMac()) {
        ensureAdminPrivilegesMacOS()
    }
    startDI(listOf(jvmMainModule, jvmVpnModule)){}
    // Get path to the current jar-file
    val encodedPath = this::class.java.protectionDomain.codeSource.location.path
    val decodedPath = URLDecoder.decode(encodedPath, StandardCharsets.UTF_8.name())
    val appDir = File(decodedPath).parentFile.absolutePath
    if (Platform.isWindows()) {
        // start device check
        val addTapDevice = AddTapDevice(KoinPlatform.getKoin().get<Logger>())
        addTapDevice.addTapDevice(appDir)
    }

    // Launch the main window and call your shared App composable.
    Window(onCloseRequest = ::exitApplication, title = "Dobby VPN 13") {
        DesktopClientTheme {
            App()
        }
    }
}