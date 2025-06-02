package com.dobby.feature.diagnostic.presentation

data class UiData(
    var ipData: IpData,
    var dnsData: IpData,
) {
    companion object {
        val EMPTY = UiData(IpData.EMPTY, IpData.EMPTY)
    }
}
