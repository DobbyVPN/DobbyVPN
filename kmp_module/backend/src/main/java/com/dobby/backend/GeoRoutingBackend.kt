package com.dobby.backend

class GeoRoutingBackend {

    external fun setGeoRoutingConf(cidrs: String): Unit

    external fun clearGeoRoutingConf(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
