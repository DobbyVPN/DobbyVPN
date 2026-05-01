package com.dobby.backend

import android.net.VpnService

class GeoRoutingBackend {

    external fun setGeoRoutingConf(cidrs: String): Unit

    external fun clearGeoRoutingConf(): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
