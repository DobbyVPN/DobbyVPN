package com.dobby.feature.authentication.domain

import android.annotation.SuppressLint
import android.content.Context
import android.location.Geocoder
import android.location.Location
import android.location.LocationManager
import android.os.Build
import android.os.CancellationSignal
import android.os.Looper
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import java.util.Locale
import kotlin.coroutines.resume

private const val LOCATION_TIMEOUT_MS = 15_000L

private const val GEOCODER_TIMEOUT_MS = 10_000L

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
        if (!Geocoder.isPresent()) {
            return emptyList()
        }
        return try {
            withTimeoutOrNull(GEOCODER_TIMEOUT_MS) {
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

            // Try GPS first (most accurate)
            if (lm.isProviderEnabled(LocationManager.GPS_PROVIDER)) {
                val gpsResult = withTimeoutOrNull(LOCATION_TIMEOUT_MS) {
                    requestLocation(lm, LocationManager.GPS_PROVIDER)
                }
                if (gpsResult != null) return gpsResult
            }

            // Fallback to NETWORK provider (faster fix, less accurate)
            if (lm.isProviderEnabled(LocationManager.NETWORK_PROVIDER)) {
                val networkResult = withTimeoutOrNull(LOCATION_TIMEOUT_MS) {
                    requestLocation(lm, LocationManager.NETWORK_PROVIDER)
                }
                if (networkResult != null) return networkResult
            }

            null
        } catch (_: Exception) {
            null
        }
    }

    @SuppressLint("MissingPermission")
    private suspend fun requestLocation(
        lm: LocationManager,
        provider: String
    ): AppLocation? {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            suspendCancellableCoroutine { cont ->
                val signal = CancellationSignal()
                cont.invokeOnCancellation { signal.cancel() }

                lm.getCurrentLocation(
                    provider,
                    signal,
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
