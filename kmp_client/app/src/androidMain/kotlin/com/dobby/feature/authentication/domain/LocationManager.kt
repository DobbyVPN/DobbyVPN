package com.dobby.feature.authentication.domain

import android.annotation.SuppressLint
import android.content.Context
import android.location.Geocoder
import android.location.Location
import android.location.LocationManager
import android.os.Build
import android.os.Looper
import kotlinx.coroutines.suspendCancellableCoroutine
import java.util.Locale
import kotlin.coroutines.resume

private lateinit var appContext: Context

fun initLocationProvider(context: Context) {
    appContext = context.applicationContext
}

actual val geocoder: AppGeocoder?
    get() = AndroidGeocoder()

actual val geolocator: AppGeolocator?
    get() = AndroidGeolocator()

actual val locationPermissionController: AppLocationPermissionController?
    get() = AndroidLocationPermissionController()

private class AndroidGeocoder : AppGeocoder {

    override suspend fun reverseGeocode(coordinates: AppCoordinates): List<AppPlace>? {
        if (!Geocoder.isPresent()) return null
        return try {
            val geocoder = Geocoder(appContext, Locale.getDefault())
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                suspendCancellableCoroutine { cont ->
                    geocoder.getFromLocation(
                        coordinates.latitude,
                        coordinates.longitude,
                        5
                    ) { addresses ->
                        cont.resume(addresses.map { AppPlace(it.countryName) })
                    }
                }
            } else {
                @Suppress("DEPRECATION")
                val addresses = geocoder.getFromLocation(
                    coordinates.latitude,
                    coordinates.longitude,
                    5
                )
                addresses?.map { AppPlace(it.countryName) }
            }
        } catch (_: Exception) {
            null
        }
    }
}

private class AndroidGeolocator : AppGeolocator {

    @SuppressLint("MissingPermission")
    override suspend fun getCurrentLocation(): AppLocation? {
        return try {
            val lm = appContext.getSystemService(Context.LOCATION_SERVICE) as LocationManager

            val provider = when {
                lm.isProviderEnabled(LocationManager.GPS_PROVIDER) -> LocationManager.GPS_PROVIDER
                lm.isProviderEnabled(LocationManager.NETWORK_PROVIDER) -> LocationManager.NETWORK_PROVIDER
                else -> return null
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                suspendCancellableCoroutine { cont ->
                    lm.getCurrentLocation(
                        provider,
                        null,                       // CancellationSignal
                        appContext.mainExecutor
                    ) { location: Location? ->
                        cont.resume(location?.toAppLocation())
                    }
                }
            } else {
                @Suppress("DEPRECATION")
                suspendCancellableCoroutine { cont ->
                    lm.requestSingleUpdate(
                        provider,
                        object : android.location.LocationListener {
                            override fun onLocationChanged(location: Location) {
                                if (cont.isActive) cont.resume(location.toAppLocation())
                            }

                            override fun onProviderDisabled(provider: String) {
                                if (cont.isActive) cont.resume(null)
                            }

                            override fun onProviderEnabled(provider: String) {}

                            @Deprecated("Deprecated in Java")
                            override fun onStatusChanged(
                                provider: String?,
                                status: Int,
                                extras: android.os.Bundle?
                            ) {
                            }
                        },
                        Looper.getMainLooper()
                    )
                }
            }
        } catch (_: Exception) {
            null
        }
    }

    private fun Location.toAppLocation() = AppLocation(
        coordinates = AppCoordinates(latitude, longitude),
        accuracy = accuracy.toDouble()
    )
}

private class AndroidLocationPermissionController : AppLocationPermissionController {

    override suspend fun requestPermission(): AuthPermissionState {
        val granted = androidx.core.content.ContextCompat.checkSelfPermission(
            appContext,
            android.Manifest.permission.ACCESS_FINE_LOCATION
        ) == android.content.pm.PackageManager.PERMISSION_GRANTED

        return if (granted) AuthPermissionState.Granted else AuthPermissionState.NotDetermined
    }
}
