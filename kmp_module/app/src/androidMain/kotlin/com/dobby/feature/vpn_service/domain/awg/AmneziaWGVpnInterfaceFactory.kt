package com.dobby.feature.vpn_service.domain.awg

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService
import android.os.Build
import android.util.Log
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.AmneziaWGConfig
import com.dobby.feature.vpn_service.DobbyVpnService
import com.dobby.feature.vpn_service.VpnInterfaceFactory

class AmneziaWGVpnInterfaceFactory(
    private val logger: Logger,
    private val amneziaWGConfgig: AmneziaWGConfig
) : VpnInterfaceFactory {

    override fun create(context: Context, vpnService: DobbyVpnService): VpnService.Builder {
        logger.log("Creating VPN Interface")
        val builder = vpnService.Builder()
            .setSession("AmneziaWG")
            .setMtu(amneziaWGConfgig.Interface.MTU?.toInt() ?: 1280)
            .addDisallowedApplication(context.packageName)

        try {
            val parts = amneziaWGConfgig.Interface.Address.split("/")
            val address = parts[0]
            val prefixLength = parts[1].toInt()
            builder.addAddress(address, prefixLength)
        } catch (e: Exception) {
            Log.e("MyVpnService", "Error: ${amneziaWGConfgig.Interface.Address}", e)
        }

        if (amneziaWGConfgig.Interface.DNS != null) {
            builder.addDnsServer(amneziaWGConfgig.Interface.DNS)
        }

        logger.log("VPN interface created: address is ${amneziaWGConfgig.Interface.Address}")

        val dnsServers = getDnsServers(context)
            .filter { it.isNotBlank() }
            .distinct()
        if (dnsServers.isNotEmpty()) {
            dnsServers.forEach { builder.addDnsServer(it) }
        } else {
            logger.log("No DNS servers from active network; using default DNS only")
        }
        amneziaWGConfgig.Peer.forEach { peerConfig ->
            peerConfig.AllowedIPs.split(",").forEach { subnet ->
                try {
                    val parts = subnet.split("/")
                    val address = parts[0]
                    val prefixLength = parts[1].toInt()
                    builder.addRoute(address, prefixLength)
                } catch (e: Exception) {
                    Log.e("MyVpnService", "Error: $subnet", e)
                }
            }
        }
        return builder
    }

    private fun getDnsServers(context: Context): List<String> {
        val dnsServers = mutableListOf<String>()

        // TODO add minSdk for the app if necessary
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
