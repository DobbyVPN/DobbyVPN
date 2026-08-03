package com.dobby.feature.vpn_service

import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit

/**
 * Observes the VPN transport using the supported callback API. `allNetworks`
 * was deprecated in API 31 and races a network changing while it is enumerated.
 *
 * The callback also reports a currently matching network after registration,
 * so this is suitable for the transition assertions in instrumentation tests.
 */
internal fun ConnectivityManager.awaitVpnNetwork(
    present: Boolean,
    timeoutMillis: Long,
    pollIntervalMillis: Long
): Network? = awaitVpnNetworkState(present, timeoutMillis, pollIntervalMillis)?.network

internal data class VpnNetworkState(
    val network: Network,
    val linkProperties: LinkProperties
)

internal fun ConnectivityManager.awaitVpnNetworkState(
    present: Boolean,
    timeoutMillis: Long,
    pollIntervalMillis: Long
): VpnNetworkState? {
    val matchingNetworks = ConcurrentHashMap.newKeySet<Network>()
    val linkPropertiesByNetwork = ConcurrentHashMap<Network, LinkProperties>()
    val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            // The registered request already filters for TRANSPORT_VPN.
            matchingNetworks.add(network)
        }

        override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) {
            if (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                matchingNetworks.add(network)
            } else {
                matchingNetworks.remove(network)
            }
        }

        override fun onLinkPropertiesChanged(network: Network, linkProperties: LinkProperties) {
            linkPropertiesByNetwork[network] = linkProperties
        }

        override fun onLost(network: Network) {
            matchingNetworks.remove(network)
            linkPropertiesByNetwork.remove(network)
        }
    }
    val request = NetworkRequest.Builder()
        .addTransportType(NetworkCapabilities.TRANSPORT_VPN)
        // NetworkRequest defaults to NOT_VPN. Keeping it would make this VPN
        // transport request contradictory and therefore impossible to match.
        .removeCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
        .build()

    registerNetworkCallback(request, callback)
    try {
        // A network callback is delivered asynchronously, including for VPNs
        // that already exist. Seed the callback state from a snapshot taken
        // after registration so an absence assertion does not have to wait for
        // a callback that will never arrive.
        val initialVpnNetwork = activeNetwork?.takeIf { network ->
            getNetworkCapabilities(network)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
        }
        initialVpnNetwork?.let { network ->
            matchingNetworks.add(network)
            getLinkProperties(network)?.let { linkPropertiesByNetwork[network] = it }
        }
        if (!present && initialVpnNetwork == null && matchingNetworks.isEmpty()) return null

        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis)
        var observedVpn = false
        do {
            // onAvailable may precede both capability and link-property
            // publication. Return only a VPN whose route state is queryable.
            val vpn = matchingNetworks.firstNotNullOfOrNull { network ->
                val isStillVpn = getNetworkCapabilities(network)
                    ?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
                if (!isStillVpn) {
                    if (!present) {
                        matchingNetworks.remove(network)
                        linkPropertiesByNetwork.remove(network)
                    }
                    null
                } else {
                    (linkPropertiesByNetwork[network] ?: getLinkProperties(network))
                        ?.let { VpnNetworkState(network, it) }
                }
            }
            if (vpn != null) observedVpn = true
            if (present && vpn != null) return vpn
            // Callback delivery is asynchronous. An empty set immediately after
            // registration does not prove absence; only a previously observed
            // VPN becoming absent, or the full observation window expiring, does.
            if (!present && observedVpn && vpn == null) return null
            if (!present && matchingNetworks.isEmpty()) return null
            Thread.sleep(pollIntervalMillis)
        } while (System.nanoTime() < deadline)
        return matchingNetworks.firstNotNullOfOrNull { network ->
            (linkPropertiesByNetwork[network] ?: getLinkProperties(network))
                ?.let { VpnNetworkState(network, it) }
        }
    } finally {
        unregisterNetworkCallback(callback)
    }
}
