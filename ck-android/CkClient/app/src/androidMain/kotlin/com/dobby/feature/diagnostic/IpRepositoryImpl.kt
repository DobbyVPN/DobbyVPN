package com.dobby.feature.diagnostic

import com.dobby.feature.diagnostic.domain.IpData
import com.dobby.feature.diagnostic.domain.IpRepository

class IpRepositoryImpl : IpRepository {
    override suspend fun getIpData(): IpData = TODO()
}
