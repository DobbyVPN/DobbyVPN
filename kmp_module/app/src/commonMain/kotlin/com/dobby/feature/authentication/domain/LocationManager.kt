package com.dobby.feature.authentication.domain

import kotlinx.coroutines.Job
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.launch
import org.koin.core.component.KoinComponent
import kotlin.math.PI
import kotlin.math.asin
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.sin
import kotlin.math.sqrt
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

    // all distances are in kilometres
    private fun maxDistanceToAirport(accuracy: Double): Double = accuracy + 1.5
    private fun maxDistanceToBorder(accuracy: Double): Double = accuracy + 6.0

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
        val nearbyLocations = getNearbyLocations(currentLocation)
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
            distance(currentLocation.coordinates, airport) <= maxDistanceToAirport(currentLocation.accuracy / 1000.0)
        }

    private const val EARTH_RADIUS: Double = 6371.0

    private fun distance(location1: AppCoordinates, location2: AppCoordinates): Double {
        val phi1 = location1.latitude * PI / 180.0
        val phi2 = location2.latitude * PI / 180.0
        val lambda1 = location1.longitude * PI / 180.0
        val lambda2 = location2.longitude * PI / 180.0
        // Haversine formula
        val a = sin((phi1 - phi2) / 2.0) * sin((phi1-  phi2) / 2.0) +
            cos(phi1) * cos(phi2) * sin((lambda1 - lambda2) / 2.0) * sin((lambda1 - lambda2) / 2.0)
        return 2 * EARTH_RADIUS * atan2(sqrt(a), sqrt(1 - a))
    }

    private fun getNearbyLocations(currentLocation: AppLocation): List<AppCoordinates> {
        val delta = maxDistanceToBorder(currentLocation.accuracy / 1000.0) / EARTH_RADIUS // angular distance
        val lat = currentLocation.coordinates.latitude
        val lon = currentLocation.coordinates.longitude
        val phi = lat * PI / 180.0
        val deltaPhi = delta * 180.0 / PI
        val deltaLambda = 2 * asin(sin(delta / 2) / cos(phi)) * 180.0 / PI
        return listOf(
            AppCoordinates(lat + deltaPhi, lon),
            AppCoordinates(lat - deltaPhi, lon),
            AppCoordinates(lat, lon + deltaLambda),
            AppCoordinates(lat, lon - deltaLambda),
            AppCoordinates(lat + sqrt(0.5) * deltaPhi, lon + sqrt(0.5) * deltaLambda),
            AppCoordinates(lat + sqrt(0.5) * deltaPhi, lon - sqrt(0.5) * deltaLambda),
            AppCoordinates(lat - sqrt(0.5) * deltaPhi, lon + sqrt(0.5) * deltaLambda),
            AppCoordinates(lat - sqrt(0.5) * deltaPhi, lon - sqrt(0.5) * deltaLambda),
        )
    }
}
