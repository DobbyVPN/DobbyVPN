package com.dobby.feature.authentication.domain

import dev.jordond.compass.geocoder.Geocoder
import dev.jordond.compass.geocoder.MobileGeocoder
import dev.jordond.compass.geolocation.Geolocator
import dev.jordond.compass.geolocation.MobileGeolocator
import dev.jordond.compass.permissions.LocationPermissionController
import dev.jordond.compass.permissions.MobileLocationPermissionController

actual val geocoder: Geocoder?
    get() = MobileGeocoder()

actual val geolocator: Geolocator?
    get() = MobileGeolocator()

actual val locationPermissionController: LocationPermissionController?
    get() = MobileLocationPermissionController()