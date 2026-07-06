package com.dobby.feature.vpn_service.grpc

import com.dobby.feature.logging.Logger
import interop.GrpcVpnLibrary
import interop.dnscache.DnsCacheLibrary
import interop.exceptions.VpnServiceStatusException

class RestartableDnsCacheGrpcLibrary(private val logger: Logger) : DnsCacheLibrary {
    override fun ClearDNSCache() {
        try {
            GrpcVpnLibrary.dnsCacheGrpcLibrary.ClearDNSCache()
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to clear DNS cache: $e")
        }
    }

    override fun SetDNSCacheEntries(entries: String, source: String): Int {
        return try {
            GrpcVpnLibrary.dnsCacheGrpcLibrary.SetDNSCacheEntries(entries, source)
        } catch (e: VpnServiceStatusException) {
            logger.log("[ERROR] Failed to set DNS cache entries source=$source: $e")
            0
        }
    }
}
