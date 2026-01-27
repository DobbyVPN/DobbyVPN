package interop

import io.grpc.ManagedChannelBuilder
import kotlinx.coroutines.runBlocking
import java.io.Closeable

class GRPCVPNLibrary : VPNLibrary, Closeable {
    private val HOST = "localhost"
    private val PORT = System.getenv("PORT")?.toInt() ?: 50051
    private val CHANNEL = ManagedChannelBuilder.forAddress(HOST, PORT).usePlaintext().build()
    private val CLIENT = GRPCVpnClient(CHANNEL)

    override fun StartOutline(key: String) {
        runBlocking { CLIENT.StartOutline(key) }
    }

    override fun StopOutline() {
        runBlocking { CLIENT.StopOutline() }
    }

    override fun StartCloakClient(localHost: String, localPort: String, config: String, udp: Boolean) {
        runBlocking { CLIENT.StartCloakClient(localHost, localPort, config, udp) }
    }

    override fun StopCloakClient() {
        runBlocking { CLIENT.StopCloakClient() }
    }

    override fun StartAwg(key: String, config: String) {
        runBlocking { CLIENT.StartAwg(key, config) }
    }

    override fun StopAwg() {
        runBlocking { CLIENT.StopAwg() }
    }

    override fun CouldStart(): Boolean {
        return runBlocking { CLIENT.CouldStart() }
    }

    override fun close() {
        this.CLIENT.close()
    }
}
