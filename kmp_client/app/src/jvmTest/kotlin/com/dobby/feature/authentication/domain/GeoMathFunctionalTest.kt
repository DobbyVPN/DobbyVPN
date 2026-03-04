package com.dobby.feature.authentication.domain

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class GeoMathFunctionalTest {

    // MARK: - distance() tests

    @Test
    fun distanceMoscowToSaintPetersburg() {
        val moscow = AppCoordinates(55.7558, 37.6173)
        val spb = AppCoordinates(59.9343, 30.3351)

        val distance = GeoMath.distance(moscow, spb)

        assertEquals(634.0, distance, 5.0)
    }

    @Test
    fun distanceNewYorkToLosAngeles() {
        val nyc = AppCoordinates(40.7128, -74.0060)
        val la = AppCoordinates(34.0522, -118.2437)

        val distance = GeoMath.distance(nyc, la)

        assertEquals(3936.0, distance, 10.0)
    }

    @Test
    fun distanceLondonToParis() {
        val london = AppCoordinates(51.5074, -0.1278)
        val paris = AppCoordinates(48.8566, 2.3522)

        val distance = GeoMath.distance(london, paris)

        assertEquals(344.0, distance, 5.0)
    }

    @Test
    fun distanceSamePointIsZero() {
        val point = AppCoordinates(55.0, 37.0)

        val distance = GeoMath.distance(point, point)

        assertEquals(0.0, distance, 0.001)
    }

    @Test
    fun distanceIsSymmetric() {
        val a = AppCoordinates(55.7558, 37.6173)
        val b = AppCoordinates(59.9343, 30.3351)

        val distanceAB = GeoMath.distance(a, b)
        val distanceBA = GeoMath.distance(b, a)

        assertEquals(distanceAB, distanceBA, 0.001)
    }

    @Test
    fun distanceAcrossEquator() {
        val north = AppCoordinates(10.0, 0.0)
        val south = AppCoordinates(-10.0, 0.0)

        val distance = GeoMath.distance(north, south)

        assertEquals(2224.0, distance, 10.0)
    }

    @Test
    fun distanceAcrossPrimeMeridian() {
        val west = AppCoordinates(51.5, -5.0)
        val east = AppCoordinates(51.5, 5.0)

        val distance = GeoMath.distance(west, east)

        assertEquals(695.0, distance, 10.0)
    }

    @Test
    fun distanceAcrossDateLine() {
        val west = AppCoordinates(0.0, 179.0)
        val east = AppCoordinates(0.0, -179.0)

        val distance = GeoMath.distance(west, east)

        assertEquals(222.0, distance, 5.0)
    }

    // MARK: - maxDistanceToAirport() tests

    @Test
    fun maxDistanceToAirportWithZeroAccuracy() {
        assertEquals(1.5, GeoMath.maxDistanceToAirport(0.0), 0.001)
    }

    @Test
    fun maxDistanceToAirportWithAccuracy() {
        assertEquals(2.5, GeoMath.maxDistanceToAirport(1.0), 0.001)
        assertEquals(11.5, GeoMath.maxDistanceToAirport(10.0), 0.001)
    }

    // MARK: - maxDistanceToBorder() tests

    @Test
    fun maxDistanceToBorderWithZeroAccuracy() {
        assertEquals(6.0, GeoMath.maxDistanceToBorder(0.0), 0.001)
    }

    @Test
    fun maxDistanceToBorderWithAccuracy() {
        assertEquals(7.0, GeoMath.maxDistanceToBorder(1.0), 0.001)
        assertEquals(16.0, GeoMath.maxDistanceToBorder(10.0), 0.001)
    }

    // MARK: - getNearbyLocations() tests

    @Test
    fun getNearbyLocationsReturns8Points() {
        val location = AppLocation(AppCoordinates(55.0, 37.0), accuracy = 100.0)

        val nearby = GeoMath.getNearbyLocations(location)

        assertEquals(8, nearby.size)
    }

    @Test
    fun getNearbyLocationsAreDistinct() {
        val location = AppLocation(AppCoordinates(55.0, 37.0), accuracy = 100.0)

        val nearby = GeoMath.getNearbyLocations(location)

        assertEquals(8, nearby.toSet().size)
    }

    @Test
    fun getNearbyLocationsAreWithinExpectedDistance() {
        val location = AppLocation(AppCoordinates(55.0, 37.0), accuracy = 1000.0)
        val expectedMaxDistance = GeoMath.maxDistanceToBorder(1.0) * 1.5

        val nearby = GeoMath.getNearbyLocations(location)

        nearby.forEach { point ->
            val distance = GeoMath.distance(location.coordinates, point)
            assertTrue(
                distance <= expectedMaxDistance,
                "Point $point is $distance km away, expected <= $expectedMaxDistance km"
            )
        }
    }

    @Test
    fun getNearbyLocationsIncludesCardinalDirections() {
        val center = AppCoordinates(55.0, 37.0)
        val location = AppLocation(center, accuracy = 1000.0)

        val nearby = GeoMath.getNearbyLocations(location)

        val hasNorth = nearby.any { it.latitude > center.latitude && kotlin.math.abs(it.longitude - center.longitude) < 0.01 }
        val hasSouth = nearby.any { it.latitude < center.latitude && kotlin.math.abs(it.longitude - center.longitude) < 0.01 }
        val hasEast = nearby.any { it.longitude > center.longitude && kotlin.math.abs(it.latitude - center.latitude) < 0.01 }
        val hasWest = nearby.any { it.longitude < center.longitude && kotlin.math.abs(it.latitude - center.latitude) < 0.01 }

        assertTrue(hasNorth, "Should have point to the north")
        assertTrue(hasSouth, "Should have point to the south")
        assertTrue(hasEast, "Should have point to the east")
        assertTrue(hasWest, "Should have point to the west")
    }

    @Test
    fun getNearbyLocationsIncludesDiagonals() {
        val center = AppCoordinates(55.0, 37.0)
        val location = AppLocation(center, accuracy = 1000.0)

        val nearby = GeoMath.getNearbyLocations(location)

        val hasNE = nearby.any { it.latitude > center.latitude && it.longitude > center.longitude }
        val hasNW = nearby.any { it.latitude > center.latitude && it.longitude < center.longitude }
        val hasSE = nearby.any { it.latitude < center.latitude && it.longitude > center.longitude }
        val hasSW = nearby.any { it.latitude < center.latitude && it.longitude < center.longitude }

        assertTrue(hasNE, "Should have point to the north-east")
        assertTrue(hasNW, "Should have point to the north-west")
        assertTrue(hasSE, "Should have point to the south-east")
        assertTrue(hasSW, "Should have point to the south-west")
    }

    @Test
    fun getNearbyLocationsWithHighAccuracyProducesCloserPoints() {
        val center = AppCoordinates(55.0, 37.0)
        val highAccuracy = AppLocation(center, accuracy = 10.0)
        val lowAccuracy = AppLocation(center, accuracy = 5000.0)

        val nearbyHigh = GeoMath.getNearbyLocations(highAccuracy)
        val nearbyLow = GeoMath.getNearbyLocations(lowAccuracy)

        val maxDistHigh = nearbyHigh.maxOf { GeoMath.distance(center, it) }
        val maxDistLow = nearbyLow.maxOf { GeoMath.distance(center, it) }

        assertTrue(maxDistHigh < maxDistLow, "High accuracy should produce closer points")
    }
}
