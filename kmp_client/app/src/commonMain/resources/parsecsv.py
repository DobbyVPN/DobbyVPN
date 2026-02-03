import csv
import airportinfo_pb2

airport_list = airportinfo_pb2.AirportList()

with open('airports.csv') as csvfile:
    csvreader = csv.reader(csvfile)
    next(csvreader)
    for row in csvreader:
        airport = airport_list.airports.add()
        name = row[3]
        lat = float(row[4])
        lon = float(row[5])
        airport.name = name
        airport.latitude_deg = lat
        airport.longitude_deg = lon

with open('../composeResources/files/airports', 'wb') as output:
    output.write(airport_list.SerializeToString())