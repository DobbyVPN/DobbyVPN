package interop

import com.dobby.vpnserver.VpnGrpcKt
import com.dobby.vpnserver.empty
import com.dobby.vpnserver.startAwgRequest
import com.dobby.vpnserver.startCloakClientRequest
import com.dobby.vpnserver.startOutlineRequest
import io.grpc.ManagedChannel
import java.io.Closeable
import java.util.concurrent.TimeUnit

class GRPCVpnClient(private val channel: ManagedChannel) : Closeable {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    suspend fun StartOutline(key: String) {
        val request = startOutlineRequest { this.config = key }
        val response = stub.startOutline(request)
        // response == Empty
    }

    suspend fun StopOutline() {
        val response = stub.stopOutline(empty { })
        // response == Empty
    }

    suspend fun StartCloakClient(localHost: String, localPort: String, config: String, udp: Boolean) {
        val request = startCloakClientRequest {
            this.localHost = localHost
            this.localPort = localPort
            this.config = config
            this.udp = udp
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
