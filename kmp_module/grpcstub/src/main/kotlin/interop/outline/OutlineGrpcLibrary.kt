package interop.outline

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.startOutlineRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class OutlineGrpcLibrary(channel: ManagedChannel) : OutlineLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun GetOutlineLastError(): String {
        return runBlocking {
            try {
                val response = stub.getOutlineLastError(empty { })

                response.error
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StartOutline(key: String): Int {
        return runBlocking {
            val request = startOutlineRequest { this.config = key }
            try {
                val response = stub.startOutline(request)

                response.result
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StopOutline() {
        return runBlocking {
            try {
                stub.stopOutline(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
