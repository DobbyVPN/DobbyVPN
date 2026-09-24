#!/usr/bin/env python3
"""Verify Android Go and C++ payloads and TrustTunnel ABI policy."""

from __future__ import annotations

import argparse
from pathlib import Path
import subprocess
import tempfile
import zipfile

from bounded_process import output_text, run_bounded_capture


ANDROID_ABIS = ("arm64-v8a", "x86_64")
TRUSTTUNNEL_ABI = "arm64-v8a"
BRIDGE_SYMBOLS = frozenset(
    {
        "dobby_vpn_set_log_callback",
        "dobby_vpn_set_protect_callback",
        "dobby_vpn_start",
        "dobby_vpn_stop",
    }
)


class NativePayloadError(ValueError):
    pass


def archive_entries(path: Path) -> set[str]:
    if not path.is_file():
        raise NativePayloadError(f"archive is unavailable: {path}")
    try:
        with zipfile.ZipFile(path) as archive:
            return {entry.filename for entry in archive.infolist() if not entry.is_dir()}
    except zipfile.BadZipFile as error:
        raise NativePayloadError(f"archive is not a valid ZIP: {path}") from error


def require_payloads(path: Path, label: str, directory: str, *, libcxx: bool) -> None:
    entries = archive_entries(path)
    for abi in ANDROID_ABIS:
        prefix = f"{directory}/{abi}/"
        if f"{prefix}libdobby_vpn.so" not in entries:
            raise NativePayloadError(f"{label} is missing the Go backend library for {abi}")
        if libcxx and f"{prefix}libc++_shared.so" not in entries:
            raise NativePayloadError(f"{label} is missing libc++_shared.so for {abi}")


def read_dynamic_symbols(readelf: Path, library: Path, abi: str) -> str:
    if not readelf.is_file():
        raise NativePayloadError(f"Android NDK llvm-readelf is unavailable: {readelf}")
    try:
        result = run_bounded_capture(
            [str(readelf), "--dyn-syms", "--wide", str(library)],
            timeout_seconds=30,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise NativePayloadError(f"llvm-readelf failed for {abi}: {error}") from error
    if result.returncode != 0:
        diagnostic = "\n".join(
            value for value in (
                output_text(result.stdout).strip(),
                output_text(result.stderr).strip(),
            ) if value
        )
        raise NativePayloadError(
            f"llvm-readelf failed for {abi} with exit code {result.returncode}: {diagnostic}"
        )
    # llvm-readelf normally writes symbols to stdout, but tool wrappers and
    # platform shims have emitted a successful symbol table on stderr. Keep
    # both streams available to the parser and never discard the non-empty
    # one merely because stdout exists as a blank value.
    stdout = output_text(result.stdout)
    stderr = output_text(result.stderr)
    if stdout and stderr:
        return stdout + stderr
    return stdout or stderr


def bridge_symbol_sets(symbols: str) -> tuple[set[str], set[str]]:
    undefined: set[str] = set()
    defined: set[str] = set()
    for line in symbols.splitlines():
        matches = {symbol for symbol in BRIDGE_SYMBOLS if symbol in line}
        if not matches:
            continue
        if " UND " in f" {line} ":
            undefined.update(matches)
        else:
            defined.update(matches)
    return defined, undefined


def verify_symbol_policy(abi: str, symbols: str) -> None:
    defined, undefined = bridge_symbol_sets(symbols)
    if undefined:
        joined = ", ".join(sorted(undefined))
        raise NativePayloadError(
            f"libdobby_vpn.so for {abi} has unresolved TrustTunnel bridge symbols: {joined}"
        )
    if abi == TRUSTTUNNEL_ABI:
        missing = BRIDGE_SYMBOLS - defined
        if missing:
            joined = ", ".join(sorted(missing))
            raise NativePayloadError(
                f"libdobby_vpn.so for {abi} is missing TrustTunnel bridge symbols: {joined}"
            )
    elif defined:
        raise NativePayloadError(
            f"libdobby_vpn.so for {abi} unexpectedly links TrustTunnel; "
            "update the ABI policy only with a packaged x86_64 bridge"
        )


def verify(apk: Path, readelf: Path) -> None:
    require_payloads(apk, "APK", "lib", libcxx=False)
    with zipfile.ZipFile(apk) as archive, tempfile.TemporaryDirectory(
        prefix="dobbyvpn-native-payloads-"
    ) as temporary:
        temporary_root = Path(temporary)
        for abi in ANDROID_ABIS:
            entry = f"lib/{abi}/libdobby_vpn.so"
            extracted = temporary_root / f"{abi}-libdobby_vpn.so"
            with archive.open(entry) as source, extracted.open("wb") as destination:
                while chunk := source.read(1024 * 1024):
                    destination.write(chunk)
            verify_symbol_policy(abi, read_dynamic_symbols(readelf, extracted, abi))


def main(arguments: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apk", type=Path, required=True)
    parser.add_argument("--readelf", type=Path, required=True)
    args = parser.parse_args(arguments)
    try:
        verify(args.apk, args.readelf)
    except NativePayloadError as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
