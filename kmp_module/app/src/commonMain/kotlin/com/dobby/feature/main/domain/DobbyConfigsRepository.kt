package com.dobby.feature.main.domain

interface DobbyConfigsRepository {

    fun getConnectionURL(): String

    fun setConnectionURL(connectionURL: String)
}
