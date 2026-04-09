package com.dobby.feature.main.domain

import com.dobby.feature.netcheck.domain.provideNetCheckConfigPath
import com.dobby.feature.netcheck.presentation.NetCheckManager
import com.dobby.outline.OutlineGo

class NetCheckManagerImpl: NetCheckManager {
    override fun start(configPath: String): String {
        return OutlineGo.netCheck(provideNetCheckConfigPath().toString()) ?: ""
    }

    override fun cancel() {
        OutlineGo.cancelNetCheck()
    }
}
