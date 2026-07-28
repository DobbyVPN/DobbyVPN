package interop.logger

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.initLoggerRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.Channel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class LoggerGrpcLibrary(channel: Channel) : LoggerLibrary {
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

    override fun InitTelemetry(endpoint: String, token: String) {
        // Compatibility no-op: never forward endpoint/token through the process.
    }

    override fun StopTelemetry() {
        // Compatibility no-op: remote telemetry is disabled.
    }

    override fun SetupTelemetryAttributes(config: String) {
        // Compatibility no-op: configuration attributes remain local.
    }
}
