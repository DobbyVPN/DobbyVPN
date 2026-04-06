package interop.healthcheck

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.checkServerAliveRequest
import com.dobby.grpcproto.empty
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class HealthCheckGrpcLibrary(channel: ManagedChannel) : HealthCheckLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

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
