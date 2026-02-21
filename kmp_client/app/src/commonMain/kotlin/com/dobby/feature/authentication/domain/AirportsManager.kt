package com.dobby.feature.authentication.domain

import kmp_client.app.generated.resources.Res
import org.jetbrains.compose.resources.ExperimentalResourceApi

object AirportsManager {

    data class Airport(
        val name: String,
        val latitude_deg: Double,
        val longitude_deg: Double,
    )

    data class AirportList(
        val airports: List<Airport>,
    )

    @OptIn(ExperimentalResourceApi::class)
    suspend fun loadAirports(): AirportList {
        val data = Res.readBytes("files/airports.csv")
        val csv = data.decodeToString()
        val airports = csv.lineSequence()
            .drop(1) // skip header
            .filter { it.isNotBlank() }
            .map { line -> parseCsvLine(line) }
            .filter { it.size >= 3 }
            .map { fields ->
                Airport(
                    name = fields[0],
                    latitude_deg = fields[1].toDoubleOrNull() ?: 0.0,
                    longitude_deg = fields[2].toDoubleOrNull() ?: 0.0,
                )
            }
            .toList()
        return AirportList(airports)
    }

    private fun parseCsvLine(line: String): List<String> {
        val fields = mutableListOf<String>()
        val current = StringBuilder()
        var inQuotes = false
        for (ch in line) {
            when {
                ch == '"' -> inQuotes = !inQuotes
                ch == ',' && !inQuotes -> {
                    fields.add(current.toString())
                    current.clear()
                }
                else -> current.append(ch)
            }
        }
        fields.add(current.toString())
        return fields
    }
}