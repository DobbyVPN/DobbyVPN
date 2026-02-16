package com.dobby.feature.authentication.domain

import dev.jordond.compass.Priority
import dev.jordond.compass.geocoder.MobileGeocoder
import dev.jordond.compass.geolocation.MobileGeolocator
import dev.jordond.compass.permissions.MobileLocationPermissionController
import dev.jordond.compass.permissions.PermissionState

actual val geocoder: AppGeocoder?
    get() = IosGeocoder()

actual val geolocator: AppGeolocator?
    get() = IosGeolocator()

actual val locationPermissionController: AppLocationPermissionController?
    get() = IosLocationPermissionController()

private class IosGeocoder : AppGeocoder {
    private val compass = MobileGeocoder()

    override suspend fun reverseGeocode(coordinates: AppCoordinates): List<AppPlace>? {
        val compassCoords = dev.jordond.compass.Coordinates(
            coordinates.latitude,
            coordinates.longitude
        )
        return compass.reverse(compassCoords).getOrNull()?.map { place ->
            AppPlace(place.country)
        }
    }
}

private class IosGeolocator : AppGeolocator {
    private val compass = MobileGeolocator()

    override suspend fun getCurrentLocation(): AppLocation? {
        val location = compass.current(Priority.HighAccuracy).getOrNull() ?: return null
        return AppLocation(
            coordinates = AppCoordinates(
                location.coordinates.latitude,
                location.coordinates.longitude
            ),
            accuracy = location.accuracy
        )
    }
}

private class IosLocationPermissionController : AppLocationPermissionController {
    private val compass = MobileLocationPermissionController()

    override suspend fun requestPermission(): AuthPermissionState {
        val status = compass.requirePermissionFor(Priority.HighAccuracy)
        return when (status) {
            PermissionState.Granted -> AuthPermissionState.Granted
            PermissionState.Denied -> AuthPermissionState.Denied
            PermissionState.NotDetermined -> AuthPermissionState.NotDetermined
            PermissionState.DeniedForever -> AuthPermissionState.Denied
        }
    }
}
