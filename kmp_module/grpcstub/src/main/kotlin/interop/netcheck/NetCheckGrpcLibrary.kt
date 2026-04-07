package interop.netcheck

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.netCheckRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class NetCheckGrpcLibrary(channel: ManagedChannel) : NetCheckLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun NetCheck(configPath: String): String {
        return runBlocking {
            val request = netCheckRequest { this.configPath = configPath }
            try {
                val response = stub.netCheck(request)

                response.error
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun CancelNetCheck() {
        return runBlocking {
            try {
                stub.cancelNetCheck(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
