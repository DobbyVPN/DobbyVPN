package com.dobby.feature.authentication.domain

import kotlinx.coroutines.Job
import org.koin.core.component.KoinComponent
import org.koin.core.component.get

expect val geocoder: AppGeocoder?
expect val geolocator: AppGeolocator?
expect val locationPermissionController: AppLocationPermissionController?

enum class RedZoneCheckResult {
    RED_ZONE, NOT_RED_ZONE, ERROR
}

object LocationManager: KoinComponent {
    val authenticationManager: AuthenticationManager = get()

    fun requestLocationPermission(endingFunc: (AuthPermissionState) -> Job) {
        authenticationManager.requireLocationPermission(endingFunc)
    }

    suspend fun inRedZone(): RedZoneCheckResult {
        val currentLocation = getLocation()
        if (currentLocation == null) {
            return RedZoneCheckResult.ERROR
        }
        val closeToBorder = closeToBorder(currentLocation)
        if (closeToBorder == null) {
            return RedZoneCheckResult.ERROR
        }
        if (closeToBorder) {
            return RedZoneCheckResult.RED_ZONE
        }
        if (closeToAirport(currentLocation)) {
            return RedZoneCheckResult.RED_ZONE
        }
        return RedZoneCheckResult.NOT_RED_ZONE
    }


    private suspend fun getLocation() = geolocator?.getCurrentLocation()

    private suspend fun closeToBorder(currentLocation: AppLocation): Boolean? {
        val geocoderResults = geocoder?.reverseGeocode(currentLocation.coordinates)?.map { place ->
            place.country
        }
        if (geocoderResults == null) {
            return null
        }
        if (geocoderResults.isEmpty()) {
            return null
        }
        val country1 = geocoderResults.first()
        if (!geocoderResults.all { country2 -> country1 == country2 }) {
            return true
        }
        val nearbyLocations = GeoMath.getNearbyLocations(currentLocation)
        for (nearbyLocation in nearbyLocations) {
            val nearbyGeocoderResults = geocoder?.reverseGeocode(nearbyLocation)?.map { place ->
                place.country
            }
            if (nearbyGeocoderResults == null) {
                continue
            }
            if (nearbyGeocoderResults.isEmpty()) {
                continue
            }
            if (!nearbyGeocoderResults.all { country2 -> country1 == country2 }) {
                return true
            }
        }
        return false
    }

    private suspend fun closeToAirport(currentLocation: AppLocation): Boolean =
        AirportsManager.loadAirports().airports.map { airport ->
            AppCoordinates(airport.latitude_deg, airport.longitude_deg)
        }.any { airport: AppCoordinates ->
            val accuracyKm = currentLocation.accuracy / 1000.0
            GeoMath.distance(currentLocation.coordinates, airport) <= GeoMath.maxDistanceToAirport(accuracyKm)
        }
}
