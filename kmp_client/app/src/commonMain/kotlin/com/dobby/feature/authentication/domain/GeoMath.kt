package com.dobby.feature.authentication.domain

import kotlin.math.PI
import kotlin.math.asin
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.sin
import kotlin.math.sqrt

object GeoMath {
    const val EARTH_RADIUS_KM: Double = 6371.0

    fun distance(location1: AppCoordinates, location2: AppCoordinates): Double {
        val phi1 = location1.latitude * PI / 180.0
        val phi2 = location2.latitude * PI / 180.0
        val lambda1 = location1.longitude * PI / 180.0
        val lambda2 = location2.longitude * PI / 180.0
        val a = sin((phi1 - phi2) / 2.0) * sin((phi1 - phi2) / 2.0) +
            cos(phi1) * cos(phi2) * sin((lambda1 - lambda2) / 2.0) * sin((lambda1 - lambda2) / 2.0)
        return 2 * EARTH_RADIUS_KM * atan2(sqrt(a), sqrt(1 - a))
    }

    fun maxDistanceToAirport(accuracyKm: Double): Double = accuracyKm + 1.5

    fun maxDistanceToBorder(accuracyKm: Double): Double = accuracyKm + 6.0

    fun getNearbyLocations(currentLocation: AppLocation): List<AppCoordinates> {
        val accuracyKm = currentLocation.accuracy / 1000.0
        val delta = maxDistanceToBorder(accuracyKm) / EARTH_RADIUS_KM
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
