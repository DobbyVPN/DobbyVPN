package interop

class VPNLibraryLoader {
    private val library: VPNLibrary = GRPCVPNLibrary()

    fun startOutline(key: String) {
        library.StartOutline(key)
    }

    fun stopOutline() {
        library.StopOutline()
    }

    fun startCloak(localHost: String, localPort: String, config: String, udp: Boolean) {
        library.StartCloakClient(localHost, localPort, config, udp)
    }

    fun stopCloak() {
        library.StopCloakClient()
    }

    fun startAwg(key: String, config: String) {
        library.StartAwg(key, config)
    }

    fun stopAwg() {
        library.StopAwg()
    }

    fun initLogger(path: String) {
        library.InitLogger(path)
    }

    fun couldStart(): Boolean {
        return library.CouldStart()
    }

    fun checkServerAlive(address: String, port: Int): Boolean {
        return library.CheckServerAlive(address, port) != 0
    }
}
