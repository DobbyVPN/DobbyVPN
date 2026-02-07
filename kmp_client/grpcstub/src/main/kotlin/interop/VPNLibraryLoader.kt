package interop

class VPNLibraryLoader {
    private val INSTANCE: VPNLibrary = GRPCVPNLibrary()

    fun startOutline(key: String) {
        try {
            INSTANCE.StartOutline(key)
        } catch (e: UnsatisfiedLinkError) {
            e.printStackTrace()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    fun stopOutline() {
        try {
            INSTANCE.StopOutline()
        } catch (e: UnsatisfiedLinkError) {
            e.printStackTrace()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    fun startCloak(localHost: String, localPort: String, config: String, udp: Boolean) {
        try {
            INSTANCE.StartCloakClient(localHost, localPort, config, udp)
        } catch (e: UnsatisfiedLinkError) {
            e.printStackTrace()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    fun stopCloak() {
        try {
            INSTANCE.StopCloakClient()
        } catch (e: UnsatisfiedLinkError) {
            e.printStackTrace()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    fun startAwg(key: String, config: String) {
        try {
            INSTANCE.StartAwg(key, config)
        } catch (e: UnsatisfiedLinkError) {
            e.printStackTrace()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    fun stopAwg() {
        try {
            INSTANCE.StopAwg()
        } catch (e: UnsatisfiedLinkError) {
            e.printStackTrace()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    fun couldStart(): Boolean {
        try {
            var res = INSTANCE.CouldStart()
            return res
        } catch (e: UnsatisfiedLinkError) {
            e.printStackTrace()
        } catch (e: Exception) {
            e.printStackTrace()
        }
        return false
    }

    fun checkServerAlive(address: String, port: Int): Boolean {
//        TODO("Not yet implemented")
        return true
    }
}
