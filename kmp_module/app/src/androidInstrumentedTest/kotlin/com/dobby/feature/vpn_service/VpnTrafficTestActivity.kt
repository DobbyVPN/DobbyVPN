package com.dobby.feature.vpn_service

import android.app.Activity
import android.os.Bundle
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress

/**
 * Test-APK activity that produces third-party app traffic for the device-wide VPN route proof.
 */
class VpnTrafficTestActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Thread(
            {
                Thread.sleep(ROUTE_SETTLE_MILLIS)
                val socket = DatagramSocket()
                try {
                    val target = InetAddress.getByName(DOCUMENTATION_ROUTE_ADDRESS)
                    repeat(PACKET_BURST_COUNT) {
                        socket.send(DatagramPacket(byteArrayOf(0x44), 1, target, DOCUMENTATION_ROUTE_PORT))
                        Thread.sleep(PACKET_INTERVAL_MILLIS)
                    }
                } finally {
                    socket.close()
                }
                runOnUiThread(::finish)
            },
            "vpn-test-packet",
        ).start()
    }

    private companion object {
        const val DOCUMENTATION_ROUTE_ADDRESS = "192.0.2.1"
        const val DOCUMENTATION_ROUTE_PORT = 33_434
        const val ROUTE_SETTLE_MILLIS = 500L
        const val PACKET_INTERVAL_MILLIS = 200L
        const val PACKET_BURST_COUNT = 5
    }
}
