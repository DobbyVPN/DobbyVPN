package interop.trusttunnel

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.startTrustTunnelRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class TrustTunnelGrpcLibrary(channel: ManagedChannel) : TrustTunnelLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun GetTrustTunnelLastError(): String {
        return runBlocking {
            try {
                val response = stub.getTrustTunnelLastError(empty { })
                response.error
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StartTrustTunnel(config: String): Int {
        return runBlocking {
            val request = startTrustTunnelRequest { this.config = config }
            try {
                val response = stub.startTrustTunnel(request)
                response.result
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StopTrustTunnel() {
        return runBlocking {
            try {
                stub.stopTrustTunnel(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
