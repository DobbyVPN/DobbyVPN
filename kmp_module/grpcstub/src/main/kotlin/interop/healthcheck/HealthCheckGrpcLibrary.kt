package interop.healthcheck

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.initHealthCheckRequest
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

    override fun GetConnectionState(): Int {
        return runBlocking {
            try {
                stub.getConnectionState(empty {}).connectionState
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun InitHealthCheck(config: String) {
        return runBlocking {
            try {
                stub.initHealthCheck(initHealthCheckRequest { this.config = config })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StartHealthCheck() {
        return runBlocking {
            try {
                stub.startHealthCheck(empty {})
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StopHealthCheck() {
        return runBlocking {
            try {
                stub.stopHealthCheck(empty {})
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
