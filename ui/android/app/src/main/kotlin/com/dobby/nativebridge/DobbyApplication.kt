package com.dobby

import android.app.Application
import com.dobby.nativebridge.NativeVpnBridge

class DobbyApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        NativeVpnBridge.recordAppStarted(this)
    }
}
