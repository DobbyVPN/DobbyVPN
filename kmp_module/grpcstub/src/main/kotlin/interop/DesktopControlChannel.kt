package interop

import io.grpc.ManagedChannel
import io.grpc.ManagedChannelBuilder
import io.grpc.netty.NettyChannelBuilder
import io.netty.channel.EventLoopGroup
import io.netty.channel.epoll.EpollDomainSocketChannel
import io.netty.channel.epoll.EpollEventLoopGroup
import io.netty.channel.kqueue.KQueueDomainSocketChannel
import io.netty.channel.kqueue.KQueueEventLoopGroup
import io.netty.channel.unix.DomainSocketAddress
import java.nio.file.Path

internal enum class DesktopControlTransport { TCP, UNIX }

internal fun desktopControlTransport(osName: String): DesktopControlTransport = when {
    osName.startsWith("Windows", ignoreCase = true) -> DesktopControlTransport.TCP
    osName.startsWith("Linux", ignoreCase = true) || osName.startsWith("Mac", ignoreCase = true) -> DesktopControlTransport.UNIX
    else -> DesktopControlTransport.TCP
}

internal fun desktopControlSocketPath(
    environment: Map<String, String> = System.getenv(),
    userHome: String = System.getProperty("user.home"),
    osName: String = System.getProperty("os.name"),
): String {
    environment["DOBBYVPN_CONTROL_SOCKET"]?.takeIf { it.isNotBlank() }?.let { return it }
    if (osName.startsWith("Mac", ignoreCase = true)) return "/var/run/dobbyvpn/control.sock"
    environment["XDG_RUNTIME_DIR"]?.takeIf { it.isNotBlank() }?.let { return Path.of(it, "DobbyVPN", "control.sock").toString() }
    val configRoot = Path.of(environment["XDG_CONFIG_HOME"] ?: Path.of(userHome, ".config").toString())
    return configRoot.resolve("DobbyVPN").resolve("control.sock").toString()
}

internal fun windowsControlTokenPath(programData: String): Path =
    Path.of(programData, "DobbyVPN", "control.token")

/** Owns the native event-loop group required by Netty Unix-domain channels. */
internal class DesktopControlChannel private constructor(
    val channel: ManagedChannel,
    private val eventLoop: EventLoopGroup?,
) {
    fun close() {
        channel.shutdown()
        eventLoop?.shutdownGracefully()
    }

    companion object {
        fun connect(port: Int): DesktopControlChannel = when (desktopControlTransport(System.getProperty("os.name"))) {
            DesktopControlTransport.TCP -> DesktopControlChannel(
                ManagedChannelBuilder.forAddress("localhost", port).usePlaintext().build(),
                null,
            )
            DesktopControlTransport.UNIX -> unixChannel(desktopControlSocketPath())
        }

        private fun unixChannel(path: String): DesktopControlChannel {
            val mac = System.getProperty("os.name").startsWith("Mac", ignoreCase = true)
            val group: EventLoopGroup = if (mac) KQueueEventLoopGroup() else EpollEventLoopGroup()
            val builder = NettyChannelBuilder.forAddress(DomainSocketAddress(path))
                .eventLoopGroup(group)
                .usePlaintext()
            if (mac) builder.channelType(KQueueDomainSocketChannel::class.java)
            else builder.channelType(EpollDomainSocketChannel::class.java)
            return DesktopControlChannel(builder.build(), group)
        }
    }
}
