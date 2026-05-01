package com.dobby.backend

import android.net.VpnService

class GoBackend {
    external fun registerVpnService(service: VpnService): Unit

    companion object {
        init {
            System.loadLibrary("backend")
        }
    }
}
