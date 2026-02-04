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
    fun StartOutline(key: String): Int
    fun StopOutline()
    fun GetOutlineLastError(): String?

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

    var lastOutlineError: String? = null
        private set

    /**
     * Starts Outline client and returns true if connection was successful.
     * On failure, returns false and sets [lastOutlineError] with error details.
     */
    fun startOutline(key: String): Boolean {
        lastOutlineError = null
        try {
            logger.log("Run key: ${maskStr(key)}")
            val result = INSTANCE.StartOutline(key)
            if (result == 0) {
                logger.log("Outline connected successfully.")
                return true
            } else {
                lastOutlineError = INSTANCE.GetOutlineLastError() ?: "Unknown error"
                logger.log("Outline connection FAILED: $lastOutlineError")
                return false
            }
        } catch (e: UnsatisfiedLinkError) {
            lastOutlineError = "Library error: ${e.message}"
            logger.log("Failed to call StartOutline: ${e.message}")
            e.printStackTrace()
            return false
        } catch (e: Exception) {
            lastOutlineError = "Exception: ${e.message}"
            logger.log("An error occurred while calling StartOutline: ${e.message}")
            e.printStackTrace()
            return false
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