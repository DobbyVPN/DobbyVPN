package com.dobby.feature.diagnostic.presentation

data class IpData(
    val ip: String,
    val city: String,
    val country: String,
) {
    companion object {
        val EMPTY = IpData("null", "null", "null")
        val LOADING = IpData("Loading...", "", "")
    }
}
