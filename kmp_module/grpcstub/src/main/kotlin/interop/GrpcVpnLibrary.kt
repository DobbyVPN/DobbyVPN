package interop

import interop.cloak.CloakGrpcLibrary
import interop.dnscache.DnsCacheGrpcLibrary
import interop.georouting.GeoroutingGrpcLibrary
import interop.healthcheck.HealthCheckGrpcLibrary
import interop.logger.LoggerGrpcLibrary
import interop.outline.OutlineGrpcLibrary
import interop.session.SessionGrpcLibrary
import interop.trusttunnel.TrustTunnelGrpcLibrary
import interop.xray.XrayGrpcLibrary
import io.grpc.ClientInterceptors
import io.grpc.Metadata
import io.grpc.stub.MetadataUtils
import java.io.Closeable
import java.nio.file.Files
import java.util.concurrent.TimeUnit

object GrpcVpnLibrary: Closeable {
    private const val PORT_ENV = "PORT"
    private const val PORT_DEFAULT = 50051
    private const val TERMINATION_TIMEOUT = 10L
    private val CONTROL_TOKEN_HEADER: Metadata.Key<String> =
        Metadata.Key.of("x-dobby-control-token", Metadata.ASCII_STRING_MARSHALLER)

    private val port = System.getenv(PORT_ENV)?.toInt() ?: PORT_DEFAULT
    private val desktopControl = DesktopControlChannel.connect(port)
    private val baseChannel = desktopControl.channel
    private val channel = if (isWindows()) {
        val headers = Metadata().apply { put(CONTROL_TOKEN_HEADER, windowsControlToken()) }
        ClientInterceptors.intercept(baseChannel, MetadataUtils.newAttachHeadersInterceptor(headers))
    } else baseChannel

    val outlineGrpcLibrary = OutlineGrpcLibrary(channel)
    val xrayGrpcLibrary = XrayGrpcLibrary(channel)
    val trustTunnelGrpcLibrary = TrustTunnelGrpcLibrary(channel)
    val cloakGrpcLibrary = CloakGrpcLibrary(channel)
    val healthCheckGrpcLibrary = HealthCheckGrpcLibrary(channel)
    val dnsCacheGrpcLibrary = DnsCacheGrpcLibrary(channel)
    val loggerGrpcLibrary = LoggerGrpcLibrary(channel)
    val georoutingGrpcLibrary = GeoroutingGrpcLibrary(channel)
    val sessionGrpcLibrary = SessionGrpcLibrary(channel)

    override fun close() {
        this.desktopControl.close()
        this.baseChannel.awaitTermination(TERMINATION_TIMEOUT, TimeUnit.SECONDS)
    }

    private fun isWindows(): Boolean = System.getProperty("os.name").startsWith("Windows", ignoreCase = true)

    private fun windowsControlToken(): String {
        val programData = checkNotNull(System.getenv("PROGRAMDATA")) {
            "Windows installation control token path is unavailable"
        }
        val path = windowsControlTokenPath(programData)
        val value = runCatching { Files.readString(path).trim() }.getOrElse {
            error("Windows installation control token is unavailable")
        }
        check(value.matches(Regex("[0-9a-fA-F]{64}"))) {
            "Windows installation control token is invalid"
        }
        return value
    }
}
