package interop.xray

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.startXrayRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class XrayGrpcLibrary(channel: ManagedChannel) : XrayLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun GetXrayLastError(): String {
        return runBlocking {
            try {
                val response = stub.getXrayLastError(empty { })
                response.error
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StartXray(config: String): Int {
        return runBlocking {
            val request = startXrayRequest { this.config = config }
            try {
                val response = stub.startXray(request)
                response.result
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StopXray() {
        return runBlocking {
            try {
                stub.stopXray(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}

