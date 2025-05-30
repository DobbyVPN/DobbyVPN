package com.dobby.feature.diagnostic.presentation

data class UiData(
    val ip: String
) {
    companion object {
        val EMPTY = UiData("N/A")
    }
}
