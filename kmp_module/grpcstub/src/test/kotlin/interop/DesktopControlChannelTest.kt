package interop

import kotlin.test.Test
import kotlin.test.assertEquals
import java.nio.file.Path

class DesktopControlChannelTest {
    @Test fun `linux and mac select unix transport`() {
        assertEquals(DesktopControlTransport.UNIX, desktopControlTransport("Linux"))
        assertEquals(DesktopControlTransport.UNIX, desktopControlTransport("Mac OS X"))
        assertEquals(DesktopControlTransport.TCP, desktopControlTransport("Windows 11"))
    }

    @Test fun `unix path honors explicit socket`() {
        assertEquals(
            "/tmp/dobby.sock",
            desktopControlSocketPath(mapOf("DOBBYVPN_CONTROL_SOCKET" to "/tmp/dobby.sock"), "/home/test", "Linux"),
        )
    }

    @Test fun `installed mac service uses system socket`() {
        assertEquals(
            "/var/run/dobbyvpn/control.sock",
            desktopControlSocketPath(emptyMap(), "/Users/test", "Mac OS X"),
        )
    }

    @Test fun `windows token uses installation wide path`() {
        assertEquals(
            Path.of("C:\\ProgramData", "DobbyVPN", "control.token"),
            windowsControlTokenPath("C:\\ProgramData"),
        )
    }
}
