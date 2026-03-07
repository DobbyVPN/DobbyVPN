package com.dobby.feature.authentication.domain

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class AirportsManagerFunctionalTest {

    @Test
    fun `parseCsvLine splits simple comma-separated values`() {
        val result = AirportsManager.parseCsvLine("JFK,40.6413,-73.7781")

        assertEquals(3, result.size)
        assertEquals("JFK", result[0])
        assertEquals("40.6413", result[1])
        assertEquals("-73.7781", result[2])
    }

    @Test
    fun `parseCsvLine handles quoted fields with commas`() {
        val result = AirportsManager.parseCsvLine("\"New York, JFK\",40.6413,-73.7781")

        assertEquals(3, result.size)
        assertEquals("New York, JFK", result[0])
        assertEquals("40.6413", result[1])
        assertEquals("-73.7781", result[2])
    }

    @Test
    fun `parseCsvLine handles empty fields`() {
        val result = AirportsManager.parseCsvLine("JFK,,")

        assertEquals(3, result.size)
        assertEquals("JFK", result[0])
        assertEquals("", result[1])
        assertEquals("", result[2])
    }

    @Test
    fun `parseCsvLine handles quoted empty string`() {
        val result = AirportsManager.parseCsvLine("JFK,\"\",40.6413")

        assertEquals(3, result.size)
        assertEquals("JFK", result[0])
        assertEquals("", result[1])
        assertEquals("40.6413", result[2])
    }

    @Test
    fun `parseAirportsFromCsv skips header line`() {
        val csv = """
            name,latitude_deg,longitude_deg
            JFK,40.6413,-73.7781
        """.trimIndent()

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertEquals(1, result.airports.size)
        assertEquals("JFK", result.airports[0].name)
    }

    @Test
    fun `parseAirportsFromCsv skips empty lines`() {
        val csv = """
            name,latitude_deg,longitude_deg
            JFK,40.6413,-73.7781

            LAX,33.9425,-118.4081

        """.trimIndent()

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertEquals(2, result.airports.size)
        assertEquals("JFK", result.airports[0].name)
        assertEquals("LAX", result.airports[1].name)
    }

    @Test
    fun `parseAirportsFromCsv skips entries with invalid coordinates`() {
        val csv = """
            name,latitude_deg,longitude_deg
            JFK,invalid,not-a-number
            LAX,33.9425,-118.4081
        """.trimIndent()

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertEquals(1, result.airports.size)
        assertEquals("LAX", result.airports[0].name)
    }

    @Test
    fun `parseAirportsFromCsv skips lines with fewer than 3 fields`() {
        val csv = """
            name,latitude_deg,longitude_deg
            JFK,40.6413,-73.7781
            incomplete,only-two
            LAX,33.9425,-118.4081
        """.trimIndent()

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertEquals(2, result.airports.size)
        assertEquals("JFK", result.airports[0].name)
        assertEquals("LAX", result.airports[1].name)
    }

    @Test
    fun `parseAirportsFromCsv parses negative coordinates correctly`() {
        val csv = """
            name,latitude_deg,longitude_deg
            SYD,-33.9461,151.1772
        """.trimIndent()

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertEquals(1, result.airports.size)
        assertEquals(-33.9461, result.airports[0].latitude_deg, 0.0001)
        assertEquals(151.1772, result.airports[0].longitude_deg, 0.0001)
    }

    @Test
    fun `parseAirportsFromCsv handles quoted airport names with special characters`() {
        val csv = """
            name,latitude_deg,longitude_deg
            "O'Hare International",41.9742,-87.9073
            "São Paulo–Guarulhos",-23.4356,-46.4731
        """.trimIndent()

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertEquals(2, result.airports.size)
        assertEquals("O'Hare International", result.airports[0].name)
        assertEquals("São Paulo–Guarulhos", result.airports[1].name)
    }

    @Test
    fun `parseAirportsFromCsv returns empty list for header-only csv`() {
        val csv = "name,latitude_deg,longitude_deg"

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertTrue(result.airports.isEmpty())
    }

    @Test
    fun `parseAirportsFromCsv returns empty list for empty csv`() {
        val csv = ""

        val result = AirportsManager.parseAirportsFromCsv(csv)

        assertTrue(result.airports.isEmpty())
    }
}
