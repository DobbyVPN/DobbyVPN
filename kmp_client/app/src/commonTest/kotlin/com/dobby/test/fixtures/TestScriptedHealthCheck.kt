package com.dobby.test.fixtures

import com.dobby.feature.diagnostic.domain.HealthCheck

class TestScriptedHealthCheck(
    private val wakeup: Int = 0,
    private val fullScript: ArrayDeque<Boolean> = ArrayDeque(),
    private val shortScript: ArrayDeque<Boolean> = ArrayDeque(),
    private val defaultShort: Boolean = true,
    private val defaultFull: Boolean = false,
) : HealthCheck {
    var shortCalls: Int = 0

    override fun shortConnectionCheckUp(): Boolean {
        shortCalls++
        return shortScript.removeFirstOrNull() ?: defaultShort
    }

    override fun fullConnectionCheckUp(): Boolean =
        fullScript.removeFirstOrNull() ?: defaultFull

    override fun checkServerAlive(address: String, port: Int): Boolean = true

    override fun getTimeToWakeUp(): Int = wakeup
}
