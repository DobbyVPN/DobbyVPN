package com.dobby.feature.vpn_service.domain.georouting

import com.dobby.feature.logging.Logger
import com.dobby.backend.GoBackendWrapper
import kotlin.math.min

class GeoRouting(
    private val logger: Logger
) {

    fun setGeoRoutingConf(paths: String) {
        logger.log("[GeoRouting][setGeoRoutingConf] paths = ${paths.take(min(paths.length, 100))}")
        GoBackendWrapper.setGeoRoutingConf(paths)
    }

    fun clearGeoRoutingConf() {
        logger.log("[GeoRouting][clearGeoRoutingConf] clear georouting config")
        GoBackendWrapper.clearGeoRoutingConf()
    }
}
