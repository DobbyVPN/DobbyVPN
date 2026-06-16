package interop.logger

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.initLoggerRequest
import com.dobby.grpcproto.initTelemetryRequest
import com.dobby.grpcproto.setupTelemetryAttributesRequest
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

    override fun InitTelemetry(endpoint: String, token: String) {
        return runBlocking {
            val request = initTelemetryRequest {
                this.endpoint = endpoint
                this.token = token
            }
            try {
                stub.initTelemetry(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun StopTelemetry() {
        return runBlocking {
            try {
                stub.stopTelemetry(empty { })
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun SetupTelemetryAttributes(config: String) {
        return runBlocking {
            val request = setupTelemetryAttributesRequest { this.config = config }
            try {
                stub.setupTelemetryAttributes(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
