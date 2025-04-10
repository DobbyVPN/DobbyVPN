package com.dobby.awg

import android.net.VpnService
import android.net.VpnService.Builder
import android.os.Build
import android.system.OsConstants
import com.dobby.awg.config.BadConfigException
import com.dobby.awg.config.Config
import com.dobby.feature.logging.Logger

class TunnelManager(private val service: VpnService, private val logger: Logger) {

    private val tunnelName = "awg0"
    var tunnelData: TunnelData =
        TunnelData(tunnelName, null, TunnelState.DOWN, -1)

    fun updateState(stringConfig: String?, state: TunnelState) {
        val config = if (stringConfig != null) {
            try {
                Config.parse(stringConfig.byteInputStream())
            } catch (e: BadConfigException) {
                logger.log("[$tunnelName] Failed: bad config: ${e.message}")

                return
            }
        } else null

        if (state == TunnelState.UP) {
            if (config == null) {
                logger.log("[$tunnelName] Failed: Empty config")

                return
            }

            if (VpnService.prepare(service) != null) {
                logger.log("[$tunnelName] Failed: VPN is not authorised")

                return
            }

            if (tunnelData.currentTunnelHandle != -1) {
                logger.log("[$tunnelName] Failed: Tunnel already up")

                return
            }

            // Build config
            val goConfig = config.toAwgUserspaceString()

            // Create the vpn tunnel with android API
            val builder: Builder = service.Builder()

            logger.log("[$tunnelName] New VPN service session")
            builder.setSession(tunnelName)

            for (addr in config.intface.addresses) {
                logger.log("[$tunnelName] Add address ${addr.address} ${addr.mask}")
                builder.addAddress(addr.address, addr.mask)
            }

            for (addr in config.intface.dnsServers) {
                logger.log("[$tunnelName] Add dns $addr")
                builder.addDnsServer(addr)
            }

            for (dnsSearchDomain in config.intface.dnsSearchDomains) {
                logger.log("[$tunnelName] Add dns search domain $dnsSearchDomain")
                builder.addSearchDomain(dnsSearchDomain)
            }

            var sawDefaultRoute = false
            for (peer in config.peers) {
                for (addr in peer.allowedIps) {
                    if (addr.mask == 0)
                        sawDefaultRoute = true

                    logger.log("[$tunnelName] Add route ${addr.address} ${addr.mask}")
                    builder.addRoute(addr.address, addr.mask)
                }
            }

            // "Kill-switch" semantics
            if (!(sawDefaultRoute && config.peers.size == 1)) {
                builder.allowFamily(OsConstants.AF_INET)
                builder.allowFamily(OsConstants.AF_INET6)
            }

            logger.log("[$tunnelName] Set MTU ${config.intface.mtu.orElse(1280)}")
            builder.setMtu(config.intface.mtu.orElse(1280))

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q)
                builder.setMetered(false)

            builder.setBlocking(true)

            val currentTunnelHandle: Int
            builder.establish().use { tun ->
                currentTunnelHandle =
                    GoBackendWrapper.awgTurnOn(tunnelName, tun!!.detachFd(), goConfig)
                logger.log("[$tunnelName] Got tunnel handle $currentTunnelHandle")
            }

            if (currentTunnelHandle < 0) {
                logger.log("[$tunnelName] tunnel activation failed")

                return
            }

            tunnelData = TunnelData(tunnelName, config, TunnelState.UP, currentTunnelHandle)

            service.protect(GoBackendWrapper.awgGetSocketV4(currentTunnelHandle))
            service.protect(GoBackendWrapper.awgGetSocketV6(currentTunnelHandle))
        } else {
            if (tunnelData.currentTunnelHandle == -1) {
                logger.log("[$tunnelName] Failed: tunnel is off")

                return
            }

            GoBackendWrapper.awgTurnOff(tunnelData.currentTunnelHandle)
            tunnelData = TunnelData(tunnelName, null, TunnelState.DOWN, -1)
        }
    }
}