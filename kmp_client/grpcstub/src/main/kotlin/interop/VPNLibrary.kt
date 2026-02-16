package interop

import interop.data.TcpPingResponce
import interop.data.UrlTestResponce

interface VPNLibrary {
    // Awg
    fun StartAwg(key: String, config: String)
    fun StopAwg()

    // Outline
    fun StartOutline(key: String)
    fun StopOutline()

    // Healthcheck
    fun StartHealthCheck(period: Int, sendMetrics: Boolean)
    fun StopHealthCheck()
    fun Status(): String
    fun TcpPing(address: String): TcpPingResponce
    fun UrlTest(url: String, standard: Int): UrlTestResponce
    fun CouldStart(): Boolean
    fun CheckServerAlive(address: String, port: Int): Int

    // Cloak
    fun StartCloakClient(localHost: String, localPort: String, config: String, udp: Boolean)
    fun StopCloakClient()

    // InitLogger
    fun InitLogger(path: String)
}
