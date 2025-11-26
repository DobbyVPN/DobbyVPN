package interop

import com.dobby.vpnserver.VpnGrpcKt.VpnCoroutineStub
import com.dobby.vpnserver.empty
import com.dobby.vpnserver.startAwgRequest
import com.dobby.vpnserver.startCloakClientRequest
import com.dobby.vpnserver.startOutlineRequest
import io.grpc.ManagedChannel
import io.grpc.ManagedChannelBuilder
import kotlinx.coroutines.runBlocking
import java.io.Closeable
import java.util.concurrent.TimeUnit


class GRPCVpnClient(private val channel: ManagedChannel) : Closeable {
    private val stub = VpnCoroutineStub(channel)

    suspend fun StartOutline(key: String) {
        val request = startOutlineRequest { this.config = key }
        val response = stub.startOutline(request)
        // response == Empty
    }

    suspend fun StopOutline() {
        val response = stub.stopOutline(empty { })
        // response == Empty
    }

    suspend fun StartCloakClient(localHost: String, localPort: String, config: String) {
        val request = startCloakClientRequest {
            this.localHost = localHost
            this.localPort = localPort
            this.config = config
        }
        val response = stub.startCloakClient(request)
        // response == Empty
    }

    suspend fun StopCloakClient() {
        val response = stub.stopCloakClient(empty { })
        // response == Empty
    }

    suspend fun StartAwg(key: String, config: String) {
        val request = startAwgRequest {
            this.tunnel = key
            this.config = config
        }
        val response = stub.startAwg(request)
        // response == Empty
    }

    suspend fun StopAwg() {
        val response = stub.stopAwg(empty { })
        // response == Empty
    }

    suspend fun CouldStart(): Boolean {
        val response = stub.couldStart(empty { })

        return response.result
    }

    override fun close() {
        channel.shutdown().awaitTermination(5, TimeUnit.SECONDS)
    }
}

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

    override fun StartCloakClient(localHost: String, localPort: String, config: String) {
        runBlocking { CLIENT.StartCloakClient(localHost, localPort, config) }
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
