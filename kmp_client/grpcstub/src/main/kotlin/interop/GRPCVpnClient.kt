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
import interop.exceptions.VPNServiceConnectionException
import io.grpc.ManagedChannel
import io.grpc.StatusException
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
        try {
            stub.startAwg(request)
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun StopAwg() {
        try {
            stub.stopAwg(empty { })
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }
    //endregion

    //region Outline
    suspend fun StartOutline(key: String) {
        val request = startOutlineRequest { this.config = key }
        try {
            stub.startOutline(request)
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun StopOutline() {
        try {
            stub.stopOutline(empty { })
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }
    //endregion

    //region Healthcheck
    suspend fun StartHealthCheck(period: Int, sendMetrics: Boolean) {
        val request = startHealthCheckRequest {
            this.period = period
            this.sendMetrics = sendMetrics
        }

        try {
            stub.startHealthCheck(request)
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun StopHealthCheck() {
        try {
            stub.stopHealthCheck(empty { })
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun Status(): String {
        try {
            val response = stub.status(empty { })

            return response.status
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun TcpPing(address: String): TcpPingResponce {
        val request = tcpPingRequest {
            this.address = address
        }

        try {
            val response = stub.tcpPing(request)

            return TcpPingResponce(result = response.result, error = response.error)
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun UrlTest(url: String, standard: Int): UrlTestResponce {
        val request = urlTestRequest {
            this.url = url
            this.standard = standard
        }

        try {
            val response = stub.urlTest(request)

            return UrlTestResponce(result = response.result, error = response.error)
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun CouldStart(): Boolean {
        try {
            val response = stub.couldStart(empty { })

            return response.result
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun CheckServerAlive(address: String, port: Int): Int {
        val request = checkServerAliveRequest {
            this.address = address
            this.port = port
        }

        try {
            val response = stub.checkServerAlive(request)

            return response.result
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
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

        try {
            stub.startCloakClient(request)
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }

    suspend fun StopCloakClient() {
        try {
            stub.stopCloakClient(empty { })
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }
    //endregion

    //region InitLogger
    suspend fun InitLogger(path: String) {
        val request = initLoggerRequest { this.path = path }

        try {
            stub.initLogger(request)
        } catch (e: StatusException) {
            throw VPNServiceConnectionException(e)
        }
    }
    //endregion

    override fun close() {
        channel.shutdown().awaitTermination(5, TimeUnit.SECONDS)
    }
}
