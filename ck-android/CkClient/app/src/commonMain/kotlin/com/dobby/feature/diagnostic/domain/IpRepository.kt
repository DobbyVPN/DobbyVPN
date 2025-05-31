package com.dobby.feature.diagnostic.domain

interface IpRepository {
    suspend fun getIpData(): IpData
    suspend fun getHostnameIpData(hostname: String): IpData
}