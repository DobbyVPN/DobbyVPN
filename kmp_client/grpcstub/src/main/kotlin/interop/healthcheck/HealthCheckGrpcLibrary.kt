package interop.healthcheck

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.checkServerAliveRequest
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.startHealthCheckRequest
import com.dobby.grpcproto.tcpPingRequest
import com.dobby.grpcproto.urlTestRequest
import interop.data.TcpPingResponse
import interop.data.UrlTestResponse
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class HealthCheckGrpcLibrary(channel: ManagedChannel) : HealthCheckLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    fun StartHealthCheck(period: Int, sendMetrics: Boolean) {
        return runBlocking {
            val request = startHealthCheckRequest {
                this.period = period
                this.sendMetrics = sendMetrics
            }
            try {
                stub.startHealthCheck(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    fun StopHealthCheck() {
        return runBlocking {
            try {
                stub.stopHealthCheck(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    fun Status(): String {
        return runBlocking {
            try {
                val response = stub.status(empty { })

                response.status
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    fun TcpPing(address: String): TcpPingResponse {
        return runBlocking {
            val request = tcpPingRequest {
                this.address = address
            }

            try {
                val response = stub.tcpPing(request)

                TcpPingResponse(result = response.result, error = response.error)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    fun UrlTest(url: String, standard: Int): UrlTestResponse {
        return runBlocking {
            val request = urlTestRequest {
                this.url = url
                this.standard = standard
            }

            try {
                val response = stub.urlTest(request)

                UrlTestResponse(result = response.result, error = response.error)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun CouldStart(): Boolean {
        return runBlocking {
            try {
                val response = stub.couldStart(empty { })

                response.result
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun CheckServerAlive(address: String, port: Int): Int {
        return runBlocking {
            val request = checkServerAliveRequest {
                this.address = address
                this.port = port
            }

            try {
                val response = stub.checkServerAlive(request)

                response.result
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
