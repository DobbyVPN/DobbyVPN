package com.dobby.test.fixtures

import com.dobby.feature.main.domain.AwgManager

class TestFakeAwgManager : AwgManager {
    override fun getAwgVersion(): String = "test"
    override fun onAwgConnect() = Unit
    override fun onAwgDisconnect() = Unit
}
