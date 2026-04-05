package com.dobby.feature.vpn_service.domain.georouting

import com.dobby.feature.logging.Logger
import com.dobby.outline.OutlineGo
import kotlin.math.min

class GeoRouting(
    private val logger: Logger
) {

    fun setGeoRoutingConf(paths: String) {
        logger.log("[GeoRouting][setGeoRoutingConf] paths = ${paths.take(min(paths.length, 100))}")
        OutlineGo.setGeoRoutingConf(paths)
    }

    fun clearGeoRoutingConf() {
        logger.log("[GeoRouting][clearGeoRoutingConf] clear georouting config")
        OutlineGo.clearGeoRoutingConf()
    }
}
