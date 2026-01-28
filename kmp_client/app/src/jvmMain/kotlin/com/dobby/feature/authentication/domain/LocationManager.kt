package com.dobby.feature.authentication.domain

import dev.jordond.compass.geocoder.Geocoder
import dev.jordond.compass.geolocation.Geolocator
import dev.jordond.compass.permissions.LocationPermissionController

actual val geocoder: Geocoder?
    get() = null

actual val geolocator: Geolocator?
    get() = null

actual val locationPermissionController: LocationPermissionController?
    get() = null