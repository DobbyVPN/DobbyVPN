package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.cloak.CloakLibrary
import interop.exceptions.VpnServiceStatusException

class RestartableCloakGrpcLibrary(private val logger: Logger) : CloakLibrary {
    override fun StartCloakClient(
        localHost: String,
        localPort: String,
        config: String,
        udp: Boolean
    ) {
        try {
            GrpcVpnLibrary.cloakGrpcLibrary.StartCloakClient(localHost, localPort, config, udp)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }

    override fun StopCloakClient() {
        try {
            GrpcVpnLibrary.cloakGrpcLibrary.StopCloakClient()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }
}
