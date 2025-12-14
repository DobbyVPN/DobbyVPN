package com.dobby.feature.authentication.domain

import app.softwork.serialization.csv.CSVFormat
import ck_client.app.generated.resources.Res
import dev.jordond.compass.Coordinates
import io.ktor.utils.io.core.String
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.launch
import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import org.jetbrains.compose.resources.ExperimentalResourceApi

/** Data from https://ourairports.com */
object AirportsList {
    var airportsCoordinates: List<Coordinates>? = null

    init {
        MainScope().launch {
            airportsCoordinates = loadAirportsCoordinates()
        }
    }

    suspend fun loadAirportsCoordinates(): List<Coordinates> {
        val airports = readCsv(loadData("files/coordinates.csv")).map { airportInfo ->
            Coordinates(airportInfo.latitude_deg, airportInfo.longitude_deg)
        }
        return airports + universities
    }

    @OptIn(ExperimentalResourceApi::class)
    private suspend fun loadData(path: String): String =
        String(Res.readBytes(path))

    @Serializable
    private data class AirportCoordinates(val latitude_deg: Double,val longitude_deg: Double)

    @OptIn(ExperimentalSerializationApi::class)
    private fun readCsv(csv: String): List<AirportCoordinates> =
        CSVFormat.decodeFromString(ListSerializer(AirportCoordinates.serializer()), csv)

    // for testing, remove this later
    private val universities = listOf(
        Coordinates(59.9385, 30.2707), // SPbU MCS
        Coordinates(59.9572, 30.3082), // ITMO university
        Coordinates(59.9287,30.3814), // Правда
        Coordinates(59.9289,30.2638), // HSE university
    )
}