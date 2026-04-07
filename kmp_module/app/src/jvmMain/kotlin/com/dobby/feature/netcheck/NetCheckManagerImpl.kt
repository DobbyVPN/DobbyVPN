package com.dobby.feature.netcheck

import com.dobby.feature.netcheck.presentation.NetCheckManager
import interop.netcheck.NetCheckLibrary

class NetCheckManagerImpl(val netCheckLibrary: NetCheckLibrary) : NetCheckManager {
    override fun start(configPath: String): String {
        return netCheckLibrary.NetCheck(configPath)
    }

    override fun cancel() {
        netCheckLibrary.CancelNetCheck()

    }
}
