package interop

import interop.awg.AwgGrpcLibrary
import interop.cloak.CloakGrpcLibrary
import interop.georouting.GeoroutingGrpcLibrary
import interop.healthcheck.HealthCheckGrpcLibrary
import interop.logger.LoggerGrpcLibrary
import interop.netcheck.NetCheckGrpcLibrary
import interop.outline.OutlineGrpcLibrary
import io.grpc.ManagedChannelBuilder
import java.io.Closeable
import java.util.concurrent.TimeUnit

object GrpcVpnLibrary: Closeable {
    private const val HOST = "localhost"
    private const val PORT_ENV = "PORT"
    private const val PORT_DEFAULT = 50051
    private const val TERMINATION_TIMEOUT = 10L

    private val port = System.getenv(PORT_ENV)?.toInt() ?: PORT_DEFAULT
    private val channel = ManagedChannelBuilder.forAddress(HOST, port).usePlaintext().build()

    val awgGrpcLibrary = AwgGrpcLibrary(channel)
    val outlineGrpcLibrary = OutlineGrpcLibrary(channel)
    val cloakGrpcLibrary = CloakGrpcLibrary(channel)
    val healthCheckGrpcLibrary = HealthCheckGrpcLibrary(channel)
    val loggerGrpcLibrary = LoggerGrpcLibrary(channel)
    val georoutingGrpcLibrary = GeoroutingGrpcLibrary(channel)
    val netCheckGrpcLibrary = NetCheckGrpcLibrary(channel)

    override fun close() {
        this.channel.shutdown().awaitTermination(TERMINATION_TIMEOUT, TimeUnit.SECONDS)
    }
}
