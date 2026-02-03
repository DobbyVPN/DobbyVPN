wget -O airports.csv https://davidmegginson.github.io/ourairports-data/airports.csv

apt-get update
apt-get install -y protobuf-compiler

python3 -m venv .venv
source .venv/bin/activate
python3 -m pip install protobuf

protoc --python_out=. airportinfo.proto

python3 parsecsv.py