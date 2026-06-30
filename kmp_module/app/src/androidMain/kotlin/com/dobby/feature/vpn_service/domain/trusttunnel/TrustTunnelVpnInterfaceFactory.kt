package com.dobby.feature.vpn_service.domain.trusttunnel

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService
import android.os.Build
import android.util.Log
import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.VpnInterfaceFactory
import com.dobby.feature.vpn_service.common.reservedBypassSubnets

class TrustTunnelVpnInterfaceFactory(
    private val logger: Logger
) : VpnInterfaceFactory {

    override fun create(context: Context, vpnService: DobbyVpnService): VpnService.Builder {
        logger.log("Creating TrustTunnel VPN Interface")
        val builder = vpnService.Builder()
            .setSession("TrustTunnel")
            .setMtu(1500)
            .addAddress("10.233.233.1", 24)
            .addDisallowedApplication(context.packageName)

        builder.addDnsServer("1.1.1.1")

        val dnsServers = getDnsServers(context)
            .filter { it.isNotBlank() }
            .distinct()

        if (dnsServers.isNotEmpty()) {
            dnsServers.forEach { builder.addDnsServer(it) }
            logger.log("Added system DNS servers to TrustTunnel Interface")
        } else {
            logger.log("No DNS servers from active network; using default DNS only")
        }

        reservedBypassSubnets.forEach { subnet ->
            val parts = subnet.split("/")
            if (parts.size != 2) {
                val msg = "Invalid bypass subnet format (expected exactly one '/'): $subnet"
                Log.e("TrustTunnelVpnInterfaceFactory", msg)
                throw IllegalArgumentException(msg)
            }
            val address = parts[0]
            val prefixLength = parts[1].toIntOrNull()
            
            if (prefixLength == null || prefixLength < 0 || prefixLength > 128) {
                val msg = "Invalid bypass subnet prefix length: $subnet"
                Log.e("TrustTunnelVpnInterfaceFactory", msg)
                throw IllegalArgumentException(msg)
            }
            
            val isIpv4 = address.contains('.')
            if (isIpv4 && prefixLength > 32) {
                val msg = "Invalid IPv4 bypass subnet prefix length: $subnet"
                Log.e("TrustTunnelVpnInterfaceFactory", msg)
                throw IllegalArgumentException(msg)
            }

            try {
                java.net.InetAddress.getByName(address)
            } catch (e: Exception) {
                val msg = "Invalid bypass subnet IP address: $subnet"
                Log.e("TrustTunnelVpnInterfaceFactory", msg, e)
                throw IllegalArgumentException(msg, e)
            }
            
            try {
                builder.addRoute(address, prefixLength)
            } catch (e: Exception) {
                val msg = "Error adding route: $subnet"
                Log.e("TrustTunnelVpnInterfaceFactory", msg, e)
                throw IllegalArgumentException(msg, e)
            }
        }

        return builder
    }

    private fun getDnsServers(context: Context): List<String> {
        val dnsServers = mutableListOf<String>()

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            runCatching {
                val connectivityManager =
                    context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
                val activeNetwork: Network? = connectivityManager.activeNetwork

                val linkProperties = connectivityManager.getLinkProperties(activeNetwork)
                val dnsAddresses = linkProperties?.dnsServers.orEmpty()
                dnsAddresses.forEach { addr ->
                    addr.hostAddress?.let { dnsServers.add(it) }
                }
            }.onFailure {
                // ignore; we'll fall back to default DNS
            }
        }
        return dnsServers
    }
}
