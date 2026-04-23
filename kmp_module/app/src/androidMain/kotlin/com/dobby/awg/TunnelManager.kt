package com.dobby.awg

import android.net.VpnService
import android.net.VpnService.Builder
import android.os.Build
import android.system.OsConstants
import com.dobby.awg.config.Config
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.AmneziaWGConfig

class TunnelManager(private val service: VpnService, private val logger: Logger) {

    private val tunnelName = "awg0"
    var tunnelData: TunnelData =
        TunnelData(tunnelName, null, TunnelState.DOWN, -1)

    private val IP_REGEX = """^(((?!25?[6-9])[12]\\d|[1-9])?\\d\\.?\\b){4}\$""".toRegex()

    fun updateState(tomlConfig: AmneziaWGConfig?, state: TunnelState) {
        if (state == TunnelState.UP) {
            if (tomlConfig == null) {
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
            val goConfig = tomlConfig.toAwgQuick()

            // Create the vpn tunnel with android API
            val builder: Builder = service.Builder()

            logger.log("[$tunnelName] New VPN service session")
            builder.setSession(tunnelName)

            for (addr in tomlConfig.Interface.Address.split(",")) {
                val (address, mask) = addr.trim().split("/", limit = 2)
                val maskInt = mask.toIntOrNull()

                if (maskInt != null) {
                    logger.log("[$tunnelName] Add address $address $mask")
                    builder.addAddress(address, maskInt)
                }
            }

            if (tomlConfig.Interface.DNS != null && IP_REGEX.matches(tomlConfig.Interface.DNS)) {
                logger.log("[$tunnelName] Add dns ${tomlConfig.Interface.DNS}")
                builder.addDnsServer(tomlConfig.Interface.DNS)
            }

            if (tomlConfig.Interface.DNS != null && !IP_REGEX.matches(tomlConfig.Interface.DNS)) {
                logger.log("[$tunnelName] Add dns search domain ${tomlConfig.Interface.DNS}")
                builder.addSearchDomain(tomlConfig.Interface.DNS)
            }

            var sawDefaultRoute = false
            for (peer in tomlConfig.Peer) {
                for (addr in peer.AllowedIPs.split(",")) {
                    val (address, m) = addr.trim().split('/')
                    val mask = m.toIntOrNull()

                    if (mask == null) {
                        logger.log("[$tunnelName] Skip route $addr")
                        continue
                    }

                    if (mask == 0)
                        sawDefaultRoute = true

                    logger.log("[$tunnelName] Add route $address $mask")
                    builder.addRoute(address, mask)
                }
            }

            // "Kill-switch" semantics
            if (!(sawDefaultRoute && tomlConfig.Peer.size == 1)) {
                builder.allowFamily(OsConstants.AF_INET)
                builder.allowFamily(OsConstants.AF_INET6)
            }

            logger.log("[$tunnelName] Set MTU ${tomlConfig.Interface.MTU ?: 1280}")
            builder.setMtu(tomlConfig.Interface.MTU?.toInt() ?: 1280)

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q)
                builder.setMetered(false)

            builder.setBlocking(true)

            val currentTunnelHandle: Int
            builder.establish().use { tun ->
                if (tun == null) {
                    logger.log("[$tunnelName] Error establishing tunnel")

                    return
                }

                currentTunnelHandle = GoBackendWrapper.awgTurnOn(tunnelName, tun.detachFd(), goConfig)
                logger.log("[$tunnelName] Got tunnel handle $currentTunnelHandle")
            }

            if (currentTunnelHandle < 0) {
                logger.log("[$tunnelName] tunnel activation failed")

                return
            }

            tunnelData = TunnelData(tunnelName, tomlConfig, TunnelState.UP, currentTunnelHandle)

            service.protect(GoBackendWrapper.awgGetSocketV4())
            service.protect(GoBackendWrapper.awgGetSocketV6())
        } else {
            if (tunnelData.currentTunnelHandle == -1) {
                logger.log("[$tunnelName] Failed: tunnel is off")

                return
            }

            GoBackendWrapper.awgTurnOff()
            tunnelData = TunnelData(tunnelName, null, TunnelState.DOWN, -1)
        }
    }
}
