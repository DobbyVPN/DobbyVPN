package interop

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.logging.domain.provideLogFilePath
import com.sun.jna.*
import java.io.File
import java.net.URLDecoder
import java.nio.charset.StandardCharsets

interface VPNLibrary : Library {
    // Outline
    fun StartOutline(key: String)
    fun StopOutline()

    // Cloak
    fun StartCloakClient(localHost: String, localPort: String, config: String)
    fun StopCloakClient()

    // Awg
    fun StartAwg(key: String)
    fun StopAwg()

    // Healthcheck
    fun CouldStart(): Boolean

    // InitLogger
    fun InitLogger(path: String)

    // CheckServerAlive
    fun CheckServerAlive(address: String, port: Int): Int
}

class VPNLibraryLoader(
    private val logger: Logger
) {
    private lateinit var INSTANCE: VPNLibrary

    init {
        try {
            val libFileName = when {
                Platform.isMac() -> "lib_macos.dylib"
                Platform.isLinux() -> "lib_linux.so"
                Platform.isWindows() -> "lib_windows.dll"
                else -> throw UnsupportedOperationException("Unsupported OS")
            }

            val encodedPath = this::class.java.protectionDomain.codeSource.location.path
            val decodedPath = File(URLDecoder.decode(encodedPath, StandardCharsets.UTF_8.name())).parentFile.parent
            // set path for windows as default

            val libPath = when {
                Platform.isMac() -> File(decodedPath, "runtime/Contents/Home/lib/$libFileName").absolutePath
                Platform.isLinux() -> File(decodedPath, "/runtime/lib/$libFileName").absolutePath
                Platform.isWindows() -> File(decodedPath, "/bin/$libFileName").absolutePath
                else -> throw UnsupportedOperationException("Unsupported OS")
            }

            logger.log("Attempting to load library from path: $libPath")
            INSTANCE = Native.load(libPath, VPNLibrary::class.java)

            logger.log("Library loaded successfully.")
            val path: String = provideLogFilePath().toString()
            logger.log("Start go logger init $path")
            initLogger(path)
            logger.log("Go logger init successfully.")
        } catch (e: Exception) {
            logger.log("Failed to load library: ${e.message}")
            e.printStackTrace()
        }
    }

    fun startOutline(key: String) {
        try {
            logger.log("Run key: ${maskStr(key)}")
            INSTANCE.StartOutline(key)
            logger.log("NewOutlineClient called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call NewOutlineClient: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling NewOutlineClient: ${e.message}")
            e.printStackTrace()
        }
    }

    fun stopOutline() {
        try {
            INSTANCE.StopOutline()
            logger.log("StopOutline called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call StopOutline: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling StopOutline: ${e.message}")
            e.printStackTrace()
        }
    }

    fun startCloak(localHost: String, localPort: String, config: String, udp: Boolean) {
        try {
            logger.log("Run localHost: $localHost; localPort: $localPort; udp: $udp")
            INSTANCE.StartCloakClient(localHost, localPort, config)
            logger.log("startCloak called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call startCloak: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling startCloak: ${e.message}")
            e.printStackTrace()
        }
    }

    fun stopCloak() {
        try {
            INSTANCE.StopCloakClient()
            logger.log("stopCloak called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call stopCloak: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling stopCloak: ${e.message}")
            e.printStackTrace()
        }
    }

    fun startAwg(key: String) {
        try {
            logger.log("Run key: ${maskStr(key)}")
            INSTANCE.StartAwg(key)
            logger.log("NewOutlineClient called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call NewOutlineClient: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling NewOutlineClient: ${e.message}")
            e.printStackTrace()
        }
    }

    fun stopAwg() {
        try {
            INSTANCE.StopAwg()
            logger.log("StopOutline called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call StopOutline: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling StopOutline: ${e.message}")
            e.printStackTrace()
        }
    }

    fun couldStart(): Boolean {
        try {
            var res = INSTANCE.CouldStart()
            logger.log("CouldStart called successfully.")
            return res
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call CouldStart: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling CouldStart: ${e.message}")
            e.printStackTrace()
        }
        return false
    }

    fun initLogger(path: String) {
        try {
            INSTANCE.InitLogger(path)
            logger.log("InitLogger called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call InitLogger: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling InitLogger: ${e.message}")
            e.printStackTrace()
        }
    }

    fun checkServerAlive(address: String, port: Int): Boolean {
        try {
            val res = INSTANCE.CheckServerAlive(address, port)
            logger.log("CheckServerAlive called successfully.")
            return res == 0
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call checkServerAlive: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling checkServerAlive: ${e.message}")
            e.printStackTrace()
        }
        return false
    }
}