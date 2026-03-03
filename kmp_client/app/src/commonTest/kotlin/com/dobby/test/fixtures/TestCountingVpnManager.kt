package com.dobby.test.fixtures

import com.dobby.feature.main.domain.VpnManager

class TestCountingVpnManager(
    var startCalls: Int = 0,
    var stopCalls: Int = 0,
    var activeTunnels: Int = 0,
) : VpnManager {
    override fun start() {
        startCalls++
        activeTunnels++
    }

    override fun stop() {
        stopCalls++
        if (activeTunnels > 0) activeTunnels--
    }
}
