package com.dobby.feature.diagnostic.presentation

data class UiData(
    val ip: String,
    val city: String,
    val country: String,
) {
    companion object {
        val EMPTY = UiData("null", "null", "null")
        val LOADING = UiData("Loading...", "", "")
    }
}
