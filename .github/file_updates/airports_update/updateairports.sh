wget -O airports.csv https://davidmegginson.github.io/ourairports-data/airports.csv

sudo apt-get update
sudo apt-get install -y protobuf-compiler

python3 -m venv .venv
source .venv/bin/activate
python3 -m pip install protobuf

protoc --python_out=. airportinfo.proto

OUTPUT_DIR="$(dirname "$0")/../../kmp_client/app/src/commonMain/composeResources/files"
mkdir -p "$OUTPUT_DIR"
python3 parsecsv.py "$OUTPUT_DIR/airports"