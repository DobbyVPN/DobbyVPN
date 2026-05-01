package com.dobby.backend

object GeoRoutingBackendWrapper {
    private val backend = GeoRoutingBackend()

    val setGeoRoutingConf = backend::setGeoRoutingConf

    val clearGeoRoutingConf = backend::clearGeoRoutingConf
}
