package com.dobby.feature.authentication.domain

data class AppCoordinates(
    val latitude: Double,
    val longitude: Double
)

data class AppLocation(
    val coordinates: AppCoordinates,
    /** Accuracy in metres. */
    val accuracy: Double
)

data class AppPlace(
    val country: String?
)

interface AppGeolocator {
    suspend fun getCurrentLocation(): AppLocation?
}

interface AppGeocoder {
    suspend fun reverseGeocode(coordinates: AppCoordinates): List<AppPlace>?
}

interface AppLocationPermissionController {
    suspend fun requestPermission(): AuthPermissionState
}
