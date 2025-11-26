package interop

interface VPNLibrary {
    // Outline
    fun StartOutline(key: String)
    fun StopOutline()

    // Cloak
    fun StartCloakClient(localHost: String, localPort: String, config: String)
    fun StopCloakClient()

    // Awg
    fun StartAwg(key: String, config: String)
    fun StopAwg()

    // Healthcheck
    fun CouldStart(): Boolean
}
