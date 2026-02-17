"""Download and extract minimal airports data (name, lat, lon) from ourairports.

Usage:
    python extract_airports.py [output_path]

If output_path is omitted, writes to the default composeResources location.
"""

import csv
import os
import sys
import tempfile
import urllib.request

SOURCE_URL = "https://davidmegginson.github.io/ourairports-data/airports.csv"
INCLUDED_TYPES = {"large_airport", "medium_airport"}

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
DEFAULT_OUTPUT = os.path.join(
    SCRIPT_DIR, "..", "..", "..",
    "kmp_client", "app", "src", "commonMain", "composeResources", "files",
    "airports.csv",
)


def main():
    output_path = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_OUTPUT
    output_path = os.path.normpath(output_path)
    os.makedirs(os.path.dirname(output_path), exist_ok=True)

    # Download full CSV to a temp file
    tmp_fd, tmp_path = tempfile.mkstemp(suffix=".csv")
    os.close(tmp_fd)
    try:
        print(f"Downloading {SOURCE_URL} ...")
        urllib.request.urlretrieve(SOURCE_URL, tmp_path)

        # Extract only the fields we need
        count = 0
        with open(tmp_path, encoding="utf-8") as infile, \
             open(output_path, "w", encoding="utf-8", newline="") as outfile:
            reader = csv.reader(infile)
            header = next(reader)
            type_idx = header.index("type")
            writer = csv.writer(outfile, lineterminator="\n")
            writer.writerow(["name", "latitude_deg", "longitude_deg"])
            for row in reader:
                if row[type_idx] in INCLUDED_TYPES:
                    writer.writerow([row[3], row[4], row[5]])
                    count += 1
    finally:
        os.remove(tmp_path)

    print(f"Extracted {count} airports to {output_path}")


if __name__ == "__main__":
    main()
