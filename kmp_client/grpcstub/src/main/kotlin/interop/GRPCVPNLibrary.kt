package interop

import interop.data.TcpPingResponce
import interop.data.UrlTestResponce
import io.grpc.ManagedChannelBuilder
import kotlinx.coroutines.runBlocking
import java.io.Closeable

class GRPCVPNLibrary : VPNLibrary, Closeable {
    private val HOST = "localhost"
    private val PORT = System.getenv("PORT")?.toInt() ?: 50051
    private val CHANNEL = ManagedChannelBuilder.forAddress(HOST, PORT).usePlaintext().build()
    private val CLIENT = GRPCVpnClient(CHANNEL)

    override fun StartOutline(key: String) {
        return runBlocking { CLIENT.StartOutline(key) }
    }

    override fun StopOutline() {
        return runBlocking { CLIENT.StopOutline() }
    }

    override fun StartHealthCheck(period: Int, sendMetrics: Boolean) {
        return runBlocking { CLIENT.StartHealthCheck(period, sendMetrics) }
    }

    override fun StopHealthCheck() {
        return runBlocking { CLIENT.StopHealthCheck() }
    }

    override fun Status(): String {
        return runBlocking { CLIENT.Status() }
    }

    override fun TcpPing(address: String): TcpPingResponce {
        return runBlocking { CLIENT.TcpPing(address) }
    }

    override fun UrlTest(url: String, standard: Int): UrlTestResponce {
        return runBlocking { CLIENT.UrlTest(url, standard) }
    }

    override fun StartCloakClient(localHost: String, localPort: String, config: String, udp: Boolean) {
        return runBlocking { CLIENT.StartCloakClient(localHost, localPort, config, udp) }
    }

    override fun StopCloakClient() {
        return runBlocking { CLIENT.StopCloakClient() }
    }

    override fun StartAwg(key: String, config: String) {
        return runBlocking { CLIENT.StartAwg(key, config) }
    }

    override fun StopAwg() {
        return runBlocking { CLIENT.StopAwg() }
    }

    override fun CouldStart(): Boolean {
        return runBlocking { CLIENT.CouldStart() }
    }

    override fun InitLogger(path: String) {
        return runBlocking { CLIENT.InitLogger(path) }
    }

    override fun CheckServerAlive(address: String, port: Int): Int {
        return runBlocking { CLIENT.CheckServerAlive(address, port) }
    }

    override fun close() {
        this.CLIENT.close()
    }
}
