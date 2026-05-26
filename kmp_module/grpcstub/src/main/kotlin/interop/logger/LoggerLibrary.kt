package interop.logger

import com.dobby.grpcproto.empty
import com.dobby.grpcproto.setupTelemetryAttributesRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

interface LoggerLibrary {
    fun InitLogger(path: String)
    fun InitTelemetry(endpoint: String)
    fun StopTelemetry()
    fun SetupTelemetryAttributes(config: String)
}
