package com.dobby.feature.vpn_service.domain.xray

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService
import android.os.Build
import android.util.Log
import com.dobby.feature.logging.Logger
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.VpnInterfaceFactory
import com.dobby.feature.vpn_service.common.addIpv6BlockingRoute
import com.dobby.feature.vpn_service.common.isIpv4Literal
import com.dobby.feature.vpn_service.common.reservedBypassSubnets

class XrayVpnInterfaceFactory(
    private val logger: Logger
) : VpnInterfaceFactory {

    override fun create(context: Context, vpnService: DobbyVpnService): VpnService.Builder {
        logger.log("Creating Xray VPN Interface")
        val builder = vpnService.Builder()
            .setSession("Xray")
            .setMtu(1500) // Adjust if necessary
            .addAddress("10.233.233.1", 24) // Dummy local IP for the TUN
            .addIpv6BlockingRoute(logger, "Xray")

        logger.log("Dobby app traffic is included in Xray VPN so Android health checks and latency probes use the tunnel")

        builder.addDnsServer("1.1.1.1")

        val dnsServers = getDnsServers(context)
            .filter { it.isNotBlank() }
            .filter { it.isIpv4Literal() }
            .distinct()

        if (dnsServers.isNotEmpty()) {
            dnsServers.forEach { builder.addDnsServer(it) }
            logger.log("Added system DNS servers to Xray Interface")
        } else {
            logger.log("No DNS servers from active network; using default DNS only")
        }

        reservedBypassSubnets.forEach { subnet ->
            try {
                val parts = subnet.split("/")
                val address = parts[0]
                val prefixLength = parts[1].toInt()
                builder.addRoute(address, prefixLength)
            } catch (e: Exception) {
                Log.e("MyVpnService", "Error: $subnet", e)
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
