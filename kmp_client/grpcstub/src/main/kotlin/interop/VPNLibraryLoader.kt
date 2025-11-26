package interop

import com.dobby.feature.logging.Logger
internal class VPNLibraryLoader(
    private val logger: Logger
) {
    private val INSTANCE: VPNLibrary = GRPCVPNLibrary()
    fun startOutline(key: String) {
        try {
            logger.log("Run key: $key")
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
            logger.log("Run localHost: $localHost; localPort: $localPort; config: $config; $udp")
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
            logger.log("Run key: $key")
            INSTANCE.StartAwg(key, key)
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
}
