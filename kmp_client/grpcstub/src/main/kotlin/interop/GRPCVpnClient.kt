package interop

import com.dobby.vpnserver.VpnGrpcKt
import com.dobby.vpnserver.checkServerAliveRequest
import com.dobby.vpnserver.empty
import com.dobby.vpnserver.initLoggerRequest
import com.dobby.vpnserver.startAwgRequest
import com.dobby.vpnserver.startCloakClientRequest
import com.dobby.vpnserver.startHealthCheckRequest
import com.dobby.vpnserver.startOutlineRequest
import com.dobby.vpnserver.tcpPingRequest
import com.dobby.vpnserver.urlTestRequest
import interop.data.TcpPingResponce
import interop.data.UrlTestResponce
import io.grpc.ManagedChannel
import java.io.Closeable
import java.util.concurrent.TimeUnit

class GRPCVpnClient(private val channel: ManagedChannel) : Closeable {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    //region Awg
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
    //endregion

    //region Outline
    suspend fun StartOutline(key: String) {
        val request = startOutlineRequest { this.config = key }
        val response = stub.startOutline(request)
        // response == Empty
    }

    suspend fun StopOutline() {
        val response = stub.stopOutline(empty { })
        // response == Empty
    }
    //endregion

    //region Healthcheck
    suspend fun StartHealthCheck(period: Int, sendMetrics: Boolean) {
        val request = startHealthCheckRequest {
            this.period = period
            this.sendMetrics = sendMetrics
        }
        val response = stub.startHealthCheck(request)
        // response == empty
    }

    suspend fun StopHealthCheck() {
        val response = stub.stopHealthCheck(empty { })
        // response == empty
    }

    suspend fun Status(): String {
        val response = stub.status(empty { })
        return response.status
    }

    suspend fun TcpPing(address: String): TcpPingResponce {
        val request = tcpPingRequest {
            this.address = address
        }
        val response = stub.tcpPing(request)

        return TcpPingResponce(result = response.result, error = response.error)
    }

    suspend fun UrlTest(url: String, standard: Int): UrlTestResponce {
        val request = urlTestRequest {
            this.url = url
            this.standard = standard
        }
        val response = stub.urlTest(request)

        return UrlTestResponce(result = response.result, error = response.error)
    }

    suspend fun CouldStart(): Boolean {
        val response = stub.couldStart(empty { })

        return response.result
    }

    suspend fun CheckServerAlive(address: String, port: Int): Int {
        val request = checkServerAliveRequest {
            this.address = address
            this.port = port
        }
        val response = stub.checkServerAlive(request)

        return response.result
    }
    //endregion

    //region Cloak
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
    //endregion

    //region InitLogger
    suspend fun InitLogger(path: String) {
        val request = initLoggerRequest { this.path = path }
        val response = stub.initLogger(request)
        // response == Empty
    }
    //endregion

    override fun close() {
        channel.shutdown().awaitTermination(5, TimeUnit.SECONDS)
    }
}
