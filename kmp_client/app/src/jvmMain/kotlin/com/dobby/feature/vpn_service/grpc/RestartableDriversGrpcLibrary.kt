package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.drivers.DriversLibrary
import interop.exceptions.VpnServiceStatusException
import interop.logger.LoggerLibrary

class RestartableDriversGrpcLibrary(private val logger: Logger) : DriversLibrary {
    override fun AddTapDevice(appDir: String) {
        try {
            GrpcVpnLibrary.driversGrpcLibrary.AddTapDevice(appDir)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to StartAwg: $e")
        }
    }
}
