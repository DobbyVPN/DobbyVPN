package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.exceptions.VpnServiceStatusException
import interop.trusttunnel.TrustTunnelLibrary

class RestartableTrustTunnelGrpcLibrary(private val logger: Logger) : TrustTunnelLibrary {
    override fun GetTrustTunnelLastError(): String {
        return try {
            GrpcVpnLibrary.trustTunnelGrpcLibrary.GetTrustTunnelLastError()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to get last TrustTunnel error: $e")
            ""
        }
    }

    override fun StartTrustTunnel(config: String): Int {
        return try {
            GrpcVpnLibrary.trustTunnelGrpcLibrary.StartTrustTunnel(config)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to start TrustTunnel: $e")
            -1
        }
    }

    override fun StopTrustTunnel() {
        try {
            GrpcVpnLibrary.trustTunnelGrpcLibrary.StopTrustTunnel()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to stop TrustTunnel: $e")
        }
    }
}
