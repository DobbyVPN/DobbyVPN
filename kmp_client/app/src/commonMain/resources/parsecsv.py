import csv

with open('airports.csv') as csvfile:
    with open('../kotlin/com/dobby/feature/authentication/domain/AirportsManager.kt', 'w') as output:
        output.write("""package com.dobby.feature.authentication.domain

import dev.jordond.compass.Coordinates

/**
* Generated source, run updateairports.sh (DobbyVPN/kmp_client/app/src/commonMain/resources/updateairports.sh)
* to fetch the latest data from https://github.com/davidmegginson/ourairports-data and re-generate this file
*/

interface AirportList {
    val airportsCoordinates: List<Coordinates>
}

""")
        output.write("""object AirportList0 : AirportList {
    override val airportsCoordinates = listOf(
""")
        csvreader = csv.reader(csvfile)
        next(csvreader)
        nrows = 0
        nlist = 0
        for row in csvreader:
            lat = row[4]
            lon = row[5]
            if lon.find('.') == -1:
                lon = lon + '.0'
            if lat.find('.') == -1:
                lat = lat + '.0'
            output.write('        Coordinates(' + lat + ', ' + lon + '),\n')
            nrows += 1
            if nrows > 3645:
                nlist += 1
                nrows = 0
                output.write("""    )
}

object AirportList""" + str(nlist) + """ : AirportList {
    override val airportsCoordinates = listOf(
""")
        output.write("""    )
}

/** Data from https://ourairports.com */
object AirportsManager {
    private val lists = listOf<AirportList>(""")
        for i in range(nlist + 1):
            output.write('AirportList' + str(i) + ', ')
        output.write(""")

    val coordinates = sequence<Coordinates> {
        for (list in lists) {
            yieldAll(list.airportsCoordinates)
        }
        yieldAll(universities)
    }

    // for testing, remove this later
    private val universities = listOf(
        Coordinates(59.9385, 30.2707), // SPbU MCS
        Coordinates(59.9572, 30.3082), // ITMO university
        Coordinates(59.9287,30.3814), // Правда
        Coordinates(59.9289,30.2638), // HSE university
    )
}""")

