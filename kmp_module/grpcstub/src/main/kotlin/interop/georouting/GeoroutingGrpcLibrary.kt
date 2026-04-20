package interop.georouting

import com.dobby.grpcproto.empty
import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.setGeoRoutingConfRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class GeoroutingGrpcLibrary(channel: ManagedChannel) : GeoroutingLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun SetGeoRoutingConf(cidrs: String) {
        return runBlocking {
            val request = setGeoRoutingConfRequest { this.cidrs = cidrs }
            try {
                stub.setGeoRoutingConf(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun ClearGeoRoutingConf() {
        return runBlocking {
            try {
                stub.clearGeoRoutingConf(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
