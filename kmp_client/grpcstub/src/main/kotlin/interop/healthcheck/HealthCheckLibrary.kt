package interop.healthcheck

import interop.data.TcpPingResponse
import interop.data.UrlTestResponse

interface HealthCheckLibrary {
    fun StartHealthCheck(period: Int, sendMetrics: Boolean)
    fun StopHealthCheck()
    fun Status(): String
    fun TcpPing(address: String): TcpPingResponse
    fun UrlTest(url: String, standard: Int): UrlTestResponse
    fun CouldStart(): Boolean
    fun CheckServerAlive(address: String, port: Int): Int
}
