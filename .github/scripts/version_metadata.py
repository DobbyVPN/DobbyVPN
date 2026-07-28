#!/usr/bin/env python3
"""Parse VERSION and derive the stable Android/F-Droid version code."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path
import re


VERSION_RE = re.compile(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)")
ANDROID_MAX_VERSION_CODE = 2_100_000_000


@dataclass(frozen=True)
class VersionMetadata:
    major: int
    minor: int
    maintenance: int

    @property
    def version_name(self) -> str:
        return f"{self.major}.{self.minor}.{self.maintenance}"

    @property
    def android_version_code(self) -> int:
        return self.major * 1_000_000 + self.minor * 1_000 + self.maintenance

    def outputs(self) -> dict[str, str]:
        return {
            "major": str(self.major),
            "minor": str(self.minor),
            "maintenance": str(self.maintenance),
            "version_name": self.version_name,
            "android_version_code": str(self.android_version_code),
        }


def parse_version(raw: str) -> VersionMetadata:
    value = raw.strip()
    match = VERSION_RE.fullmatch(value)
    if not match:
        raise ValueError("version must use canonical X.Y.Z numeric format")

    metadata = VersionMetadata(*(int(part) for part in match.groups()))
    if metadata.minor >= 1_000 or metadata.maintenance >= 1_000:
        raise ValueError("minor and maintenance components must be below 1000")
    if not 1 <= metadata.android_version_code <= ANDROID_MAX_VERSION_CODE:
        raise ValueError("derived Android versionCode is outside the supported range")
    return metadata


def main() -> int:
    parser = argparse.ArgumentParser()
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--version")
    source.add_argument("--version-file", type=Path)
    parser.add_argument("--github-output", type=Path)
    parser.add_argument("--field", choices=VersionMetadata(1, 2, 3).outputs())
    args = parser.parse_args()

    raw = args.version if args.version is not None else args.version_file.read_text()
    metadata = parse_version(raw)
    outputs = metadata.outputs()

    if args.github_output:
        with args.github_output.open("a", encoding="utf-8") as output:
            for key, value in outputs.items():
                output.write(f"{key}={value}\n")
    if args.field:
        print(outputs[args.field])
    elif not args.github_output:
        for key, value in outputs.items():
            print(f"{key}={value}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
