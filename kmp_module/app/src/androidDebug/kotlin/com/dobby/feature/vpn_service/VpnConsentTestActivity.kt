package com.dobby.feature.vpn_service

import android.app.Activity
import android.net.VpnService
import android.os.Bundle
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress

/**
 * Debug-only foreground host for instrumentation that exercises Android's real VPN consent UI.
 * Release APKs do not contain this activity.
 */
class VpnConsentTestActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (intent.action == ACTION_SEND_DOCUMENTATION_PACKET) {
            Thread(
                {
                    DatagramSocket().use { socket ->
                        val target = InetAddress.getByName(DOCUMENTATION_ROUTE_ADDRESS)
                        socket.send(DatagramPacket(byteArrayOf(0x44), 1, target, DOCUMENTATION_ROUTE_PORT))
                    }
                    runOnUiThread(::finish)
                },
                "vpn-test-packet",
            ).start()
            return
        }
        val consentIntent = VpnService.prepare(this)
        if (consentIntent == null) {
            finish()
        } else {
            startActivityForResult(consentIntent, VPN_CONSENT_REQUEST)
        }
    }

    @Suppress("DEPRECATION")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: android.content.Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == VPN_CONSENT_REQUEST) finish()
    }

    companion object {
        const val ACTION_SEND_DOCUMENTATION_PACKET =
            "com.dobby.feature.vpn_service.action.SEND_DOCUMENTATION_PACKET"

        private const val VPN_CONSENT_REQUEST = 1
        private const val DOCUMENTATION_ROUTE_ADDRESS = "192.0.2.1"
        private const val DOCUMENTATION_ROUTE_PORT = 33_434
    }
}
