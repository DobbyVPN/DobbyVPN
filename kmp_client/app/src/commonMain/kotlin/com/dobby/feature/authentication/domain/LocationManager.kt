package com.dobby.feature.authentication.domain

import dev.jordond.compass.Coordinates
import dev.jordond.compass.Location
import dev.jordond.compass.Priority
import dev.jordond.compass.geocoder.Geocoder
import dev.jordond.compass.geolocation.Geolocator
import dev.jordond.compass.permissions.LocationPermissionController
import dev.jordond.compass.permissions.PermissionState
import kotlin.math.PI
import kotlin.math.asin
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.sin
import kotlin.math.sqrt

expect val geocoder: Geocoder?
expect val geolocator: Geolocator?
expect val locationPermissionController: LocationPermissionController?

enum class RedZoneCheckResult {
    RED_ZONE, NOT_RED_ZONE, ERROR
}

object LocationManager {
    suspend fun requestLocationPermission(): PermissionState {
        if (locationPermissionController == null) {
            return PermissionState.NotDetermined
        }
        return locationPermissionController!!.requirePermissionFor(Priority.HighAccuracy)
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

    private suspend fun getLocation() = geolocator?.current(Priority.HighAccuracy)?.getOrNull()

    private suspend fun closeToBorder(currentLocation: Location): Boolean? {
        val geocoderResults = geocoder?.reverse(currentLocation.coordinates)?.getOrNull()?.map { place ->
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
            val nearbyGeocoderResults = geocoder?.reverse(nearbyLocation)?.getOrNull()?.map { place ->
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

    private suspend fun closeToAirport(currentLocation: Location): Boolean =
        AirportsManager.coordinates.any { airport: Coordinates ->
            distance(currentLocation.coordinates, airport) <= maxDistanceToAirport(currentLocation.accuracy / 1000.0)
        }

    private const val EARTH_RADIUS: Double = 6371.0

    private fun distance(location1: Coordinates, location2: Coordinates): Double {
        val phi1 = location1.latitude * PI / 180.0
        val phi2 = location2.latitude * PI / 180.0
        val lambda1 = location1.longitude * PI / 180.0
        val lambda2 = location2.longitude * PI / 180.0
        // Haversine formula
        val a = sin((phi1 - phi2) / 2.0) * sin((phi1-  phi2) / 2.0) +
            cos(phi1) * cos(phi2) * sin((lambda1 - lambda2) / 2.0) * sin((lambda1 - lambda2) / 2.0)
        return 2 * EARTH_RADIUS * atan2(sqrt(a), sqrt(1 - a))
    }

    private fun getNearbyLocations(currentLocation: Location): List<Coordinates> {
        val delta = maxDistanceToBorder(currentLocation.accuracy / 1000.0) / EARTH_RADIUS // angular distance
        val lat = currentLocation.coordinates.latitude
        val lon = currentLocation.coordinates.longitude
        val phi = lat * PI / 180.0
        val deltaPhi = delta * 180.0 / PI
        val deltaLambda = 2 * asin(sin(delta / 2) / cos(phi)) * 180.0 / PI
        return listOf(
            Coordinates(lat + deltaPhi, lon),
            Coordinates(lat - deltaPhi, lon),
            Coordinates(lat, lon + deltaLambda),
            Coordinates(lat, lon - deltaLambda),
            Coordinates(lat + sqrt(0.5) * deltaPhi, lon + sqrt(0.5) * deltaLambda),
            Coordinates(lat + sqrt(0.5) * deltaPhi, lon - sqrt(0.5) * deltaLambda),
            Coordinates(lat - sqrt(0.5) * deltaPhi, lon + sqrt(0.5) * deltaLambda),
            Coordinates(lat - sqrt(0.5) * deltaPhi, lon - sqrt(0.5) * deltaLambda),
        )
    }
}