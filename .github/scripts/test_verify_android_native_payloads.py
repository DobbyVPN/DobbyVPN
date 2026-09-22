from __future__ import annotations

import importlib.util
from pathlib import Path
import subprocess
import zipfile

import pytest


SPEC = importlib.util.spec_from_file_location(
    "verify_android_native_payloads",
    Path(__file__).with_name("verify_android_native_payloads.py"),
)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def _archive(path: Path, entries: set[str]) -> None:
    with zipfile.ZipFile(path, "w") as archive:
        for entry in entries:
            archive.writestr(entry, b"native")


def test_missing_archive_entry_is_rejected(tmp_path: Path) -> None:
    archive = tmp_path / "runtime.aar"
    _archive(archive, {"lib/arm64-v8a/libdobby_vpn.so"})
    with pytest.raises(MODULE.NativePayloadError, match="x86_64"):
        MODULE.require_payloads(archive, "Go/Fyne APK", "lib", libcxx=False)


def test_defined_and_undefined_symbols_are_distinguished() -> None:
    lines = "\n".join(
        (
            "1: 0 0 FUNC GLOBAL DEFAULT 12 dobby_vpn_start",
            "2: 0 0 FUNC GLOBAL DEFAULT UND dobby_vpn_stop",
        )
    )
    defined, undefined = MODULE.bridge_symbol_sets(lines)
    assert defined == {"dobby_vpn_start"}
    assert undefined == {"dobby_vpn_stop"}
    with pytest.raises(MODULE.NativePayloadError, match="unresolved"):
        MODULE.verify_symbol_policy("arm64-v8a", lines)


def test_unsupported_abi_must_not_define_trusttunnel_symbols() -> None:
    symbols = "1: 0 0 FUNC GLOBAL DEFAULT 12 dobby_vpn_start\n"
    with pytest.raises(MODULE.NativePayloadError, match="unexpectedly links"):
        MODULE.verify_symbol_policy("x86_64", symbols)


def test_readelf_failure_is_reported(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    readelf = tmp_path / "llvm-readelf"
    readelf.write_text("synthetic", encoding="utf-8")
    library = tmp_path / "libdobby_vpn.so"
    library.write_bytes(b"synthetic")
    monkeypatch.setattr(
        MODULE,
        "run_bounded_capture",
        lambda *_args, **_kwargs: subprocess.CompletedProcess([], 9, "bad ELF", ""),
    )
    with pytest.raises(MODULE.NativePayloadError, match="exit code 9.*bad ELF"):
        MODULE.read_dynamic_symbols(readelf, library, "arm64-v8a")


def test_successful_readelf_symbols_are_kept_when_written_to_stderr(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch,
) -> None:
    readelf = tmp_path / "llvm-readelf"
    readelf.write_text("synthetic", encoding="utf-8")
    library = tmp_path / "libdobby_vpn.so"
    library.write_bytes(b"synthetic")
    monkeypatch.setattr(
        MODULE,
        "run_bounded_capture",
        lambda *_args, **_kwargs: subprocess.CompletedProcess([], 0, "", "symbol table\n"),
    )
    assert MODULE.read_dynamic_symbols(readelf, library, "arm64-v8a") == "symbol table\n"
