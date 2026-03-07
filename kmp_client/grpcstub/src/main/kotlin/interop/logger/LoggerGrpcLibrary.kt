package interop.logger

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.initLoggerRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class LoggerGrpcLibrary(channel: ManagedChannel) : LoggerLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun InitLogger(path: String) {
        return runBlocking {
            val request = initLoggerRequest { this.path = path }
            try {
                stub.initLogger(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
