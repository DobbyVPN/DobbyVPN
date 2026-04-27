package com.dobby.feature.main.domain

interface DobbyConfigsRepositoryNetCheck {
    fun getNetCheckConfig(): String

    fun setNetCheckConfig(config: String)
}


