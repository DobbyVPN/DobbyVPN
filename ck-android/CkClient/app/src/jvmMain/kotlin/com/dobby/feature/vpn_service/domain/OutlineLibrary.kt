package interop

import com.dobby.feature.logging.Logger
import com.sun.jna.Library
import com.sun.jna.Native
import com.sun.jna.Platform
import com.sun.jna.NativeLibrary
import java.io.File
import java.net.URLDecoder
import java.nio.charset.StandardCharsets

interface OutlineLibrary : Library {
    fun StartOutline(key: String)
    fun StopOutline()
}

internal class OutlineLib(
    private val logger: Logger
) {
    private var INSTANCE: OutlineLibrary? = null

    init {
        try {
            val libName = when {
                Platform.isMac() -> "outline"
                Platform.isLinux() -> "outline"
                Platform.isWindows() -> "outline"
                else -> throw UnsupportedOperationException("Unsupported OS")
            }

            val libExtension = when {
                Platform.isMac() -> ".dylib"
                Platform.isLinux() -> ".so"
                Platform.isWindows() -> ".dll"
                else -> ""
            }

            val architecture = System.getProperty("os.arch")
            val libFileName = when {
                Platform.isMac() && architecture.contains("aarch64") -> "lib${libName}_arm64$libExtension"
                Platform.isMac() && architecture.contains("x86_64") -> "lib${libName}_x86_64$libExtension"
                Platform.isLinux() -> "lib${libName}_linux$libExtension"
                Platform.isWindows() -> "lib${libName}_windows$libExtension"
                else -> throw UnsupportedOperationException("Unsupported architecture")
            }

            val encodedPath = this::class.java.protectionDomain.codeSource.location.path
            val decodedPath = File(URLDecoder.decode(encodedPath, StandardCharsets.UTF_8.name())).parentFile.parent
            // set path for windows as default
            var libPath = File(decodedPath, "/bin/$libFileName").absolutePath

            if (Platform.isLinux()) {
                libPath = File(decodedPath, "/runtime/lib/$libFileName").absolutePath
            }

            if (Platform.isMac()) {
                libPath = File(decodedPath, "runtime/Contents/Home/lib/$libFileName").absolutePath
            }

            logger.log("Attempting to load library from path: $libPath")
            val nativeLibrary = NativeLibrary.getInstance(libPath)
            INSTANCE = Native.load(libPath, OutlineLibrary::class.java)

            logger.log("Library loaded successfully.")
        } catch (e: Exception) {
            logger.log("Failed to load library: ${e.message}")
            e.printStackTrace()
        }
    }

    fun startOutline(key: String) {
        try {
            if (INSTANCE == null) {
                logger.log("Library not loaded. Cannot call StartOutline.")
                return
            }
            INSTANCE!!.StartOutline(key)
            logger.log("StartOutline called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call StartOutline: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling StartOutline: ${e.message}")
            e.printStackTrace()
        }
    }

    fun stopOutline() {
        try {
            if (INSTANCE == null) {
                logger.log("Library not loaded. Cannot call StopOutline.")
                return
            }
            INSTANCE!!.StopOutline()
            logger.log("StopOutline called successfully.")
        } catch (e: UnsatisfiedLinkError) {
            logger.log("Failed to call StopOutline: ${e.message}")
            e.printStackTrace()
        } catch (e: Exception) {
            logger.log("An error occurred while calling StopOutline: ${e.message}")
            e.printStackTrace()
        }
    }
}