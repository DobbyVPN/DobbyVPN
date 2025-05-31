package com.dobby.feature.diagnostic.domain

interface IpRepository {
    suspend fun getIpData(): IpData
}