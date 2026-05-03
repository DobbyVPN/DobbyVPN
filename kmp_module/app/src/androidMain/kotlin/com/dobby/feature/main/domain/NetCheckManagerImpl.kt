package com.dobby.feature.main.domain

import com.dobby.feature.netcheck.domain.provideNetCheckConfigPath
import com.dobby.feature.netcheck.presentation.NetCheckManager
import com.dobby.outline.OutlineGo

class NetCheckManagerImpl: NetCheckManager {
    override fun start(): String {
        val configPath = provideNetCheckConfigPath().toString()
        return OutlineGo.netCheck(configPath) ?: ""
    }

    override fun cancel() {
        OutlineGo.cancelNetCheck()
    }
}
