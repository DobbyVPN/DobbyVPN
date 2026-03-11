package interop.awg

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.startAwgRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class AwgGrpcLibrary(channel: ManagedChannel) : AwgLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun StartAwg(key: String, config: String) {
        return runBlocking {
            val request = startAwgRequest {
                tunnel = key
                this.config = config
            }
            try {
                stub.startAwg(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StopAwg() {
        return runBlocking {
            try {
                stub.stopAwg(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
