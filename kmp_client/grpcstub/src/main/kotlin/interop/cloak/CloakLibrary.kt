package interop.cloak

interface CloakLibrary {
    fun StartCloakClient(localHost: String, localPort: String, config: String, udp: Boolean)
    fun StopCloakClient()
}
