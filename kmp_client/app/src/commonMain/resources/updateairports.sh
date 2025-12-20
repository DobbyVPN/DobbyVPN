wget -O airports.csv https://davidmegginson.github.io/ourairports-data/airports.csv

protoc --python_out=. airportinfo.proto

python3 -m venv .venv
source .venv/bin/activate
pip install protobuf
python3 parsecsv.py