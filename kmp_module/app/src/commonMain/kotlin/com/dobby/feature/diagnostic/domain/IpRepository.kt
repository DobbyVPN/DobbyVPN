package com.dobby.feature.diagnostic.domain

interface IpRepository {
    fun getIpData(): IpData
    fun getHostnameIpData(hostname: String): IpData
}