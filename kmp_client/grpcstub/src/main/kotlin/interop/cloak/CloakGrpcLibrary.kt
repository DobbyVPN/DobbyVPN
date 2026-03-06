package interop.cloak

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.startCloakClientRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class CloakGrpcLibrary(channel: ManagedChannel) : CloakLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun StartCloakClient(
        localHost: String,
        localPort: String,
        config: String,
        udp: Boolean
    ) {
        return runBlocking {
            val request = startCloakClientRequest {
                this.localHost = localHost
                this.localPort = localPort
                this.config = config
                this.udp = udp
            }
            try {
                stub.startCloakClient(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StopCloakClient() {
        return runBlocking {
            try {
                stub.stopCloakClient(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
