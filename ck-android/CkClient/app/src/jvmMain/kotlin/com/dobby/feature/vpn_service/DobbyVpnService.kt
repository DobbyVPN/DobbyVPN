package com.dobby.feature.vpn_service

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import interop.OutlineLib
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.launch

internal class DobbyVpnService(
    private val dobbyConfigsRepository: DobbyConfigsRepository,
    private val logger: Logger,
    private val outlineLib: OutlineLib
) {

    fun startService() {
        when(dobbyConfigsRepository.getVpnInterface()) {
            VpnInterface.CLOAK_OUTLINE -> startCloakOutline()
            VpnInterface.AMNEZIA_WG -> startAwg()
        }
    }

    fun stopService() {
    }


    fun startCloakOutline() {
        val apiKey = dobbyConfigsRepository.getOutlineKey()
        logger.log("Outline key: " + apiKey)

        GlobalScope.launch(Dispatchers.IO) {
            outlineLib.startOutline(apiKey)
        }
        logger.log("End outline")
    }

    fun startAwg() {

    }

}