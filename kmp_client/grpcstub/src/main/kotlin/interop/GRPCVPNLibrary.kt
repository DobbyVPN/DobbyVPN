package interop

import interop.data.TcpPingResponce
import interop.data.UrlTestResponce
import io.grpc.ManagedChannelBuilder
import kotlinx.coroutines.runBlocking
import java.io.Closeable

open class GRPCVPNLibrary : VPNLibrary, Closeable {
    private val host = "localhost"
    private val port = System.getenv("PORT")?.toInt() ?: 50051
    private val channel = ManagedChannelBuilder.forAddress(host, port).usePlaintext().build()
    private val client = GRPCVpnClient(channel)

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun GetOutlineLastError(): String {
        return runBlocking { client.GetOutlineLastError() }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StartOutline(key: String): Int {
        return runBlocking { client.StartOutline(key) }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StopOutline() {
        return runBlocking { client.StopOutline() }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StartHealthCheck(period: Int, sendMetrics: Boolean) {
        return runBlocking { client.StartHealthCheck(period, sendMetrics) }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StopHealthCheck() {
        return runBlocking { client.StopHealthCheck() }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun Status(): String {
        return runBlocking { client.Status() }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun TcpPing(address: String): TcpPingResponce {
        return runBlocking { client.TcpPing(address) }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun UrlTest(url: String, standard: Int): UrlTestResponce {
        return runBlocking { client.UrlTest(url, standard) }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StartCloakClient(localHost: String, localPort: String, config: String, udp: Boolean) {
        return runBlocking { client.StartCloakClient(localHost, localPort, config, udp) }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StopCloakClient() {
        return runBlocking { client.StopCloakClient() }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StartAwg(key: String, config: String) {
        return runBlocking { client.StartAwg(key, config) }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun StopAwg() {
        return runBlocking { client.StopAwg() }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun CouldStart(): Boolean {
        return runBlocking { client.CouldStart() }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun InitLogger(path: String) {
        return runBlocking { client.InitLogger(path) }
    }

    /**
     * @throws interop.exceptions.VPNServiceConnectionException
     */
    override fun CheckServerAlive(address: String, port: Int): Int {
        return runBlocking { client.CheckServerAlive(address, port) }
    }

    override fun close() {
        this.client.close()
    }
}
