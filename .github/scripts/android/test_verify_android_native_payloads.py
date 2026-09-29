from __future__ import annotations

import unittest

from verify_android_native_payloads import (
    ANDROID_ABIS,
    BRIDGE_SYMBOLS,
    NativePayloadError,
    undefined_cpp_runtime_symbols,
    verify_symbol_policy,
)


def symbol_table(*, undefined: tuple[str, ...] = ()) -> str:
    lines = [
        f"  1: 00000000 0 FUNC GLOBAL DEFAULT 12 {symbol}"
        for symbol in sorted(BRIDGE_SYMBOLS)
    ]
    lines.extend(
        f"  {index + 2}: 00000000 0 FUNC GLOBAL DEFAULT UND {symbol}"
        for index, symbol in enumerate(undefined)
    )
    return "\n".join(lines)


class AndroidNativePayloadPolicyTests(unittest.TestCase):
    def test_trusttunnel_bridge_is_required_for_every_packaged_abi(self) -> None:
        for abi in ANDROID_ABIS:
            with self.subTest(abi=abi):
                verify_symbol_policy(abi, symbol_table())

    def test_missing_bridge_symbol_fails_for_every_abi(self) -> None:
        symbols = symbol_table().replace(" dobby_vpn_start", " missing_start", 1)
        for abi in ANDROID_ABIS:
            with self.subTest(abi=abi), self.assertRaisesRegex(
                NativePayloadError, "missing TrustTunnel bridge symbols"
            ):
                verify_symbol_policy(abi, symbols)

    def test_unresolved_bridge_symbol_fails(self) -> None:
        with self.assertRaisesRegex(NativePayloadError, "unresolved TrustTunnel bridge symbols"):
            verify_symbol_policy("arm64-v8a", symbol_table(undefined=("dobby_vpn_start",)))

    def test_android_crash_symbol_is_rejected_as_unresolved_runtime_dependency(self) -> None:
        symbols = symbol_table(undefined=("__cxa_init_primary_exception",))
        self.assertEqual(
            undefined_cpp_runtime_symbols(symbols),
            {"__cxa_init_primary_exception"},
        )
        with self.assertRaisesRegex(NativePayloadError, "__cxa_init_primary_exception"):
            verify_symbol_policy("arm64-v8a", symbols)

    def test_versioned_android_crash_symbol_is_rejected(self) -> None:
        symbols = (
            symbol_table(undefined=("__cxa_init_primary_exception@LIBC",))
            + " (6)"
        )
        self.assertEqual(
            undefined_cpp_runtime_symbols(symbols),
            {"__cxa_init_primary_exception"},
        )
        with self.assertRaisesRegex(NativePayloadError, "__cxa_init_primary_exception"):
            verify_symbol_policy("x86_64", symbols)

    def test_bionic_cxa_atexit_import_is_not_misclassified(self) -> None:
        self.assertEqual(
            undefined_cpp_runtime_symbols(symbol_table(undefined=("__cxa_atexit",))),
            set(),
        )


if __name__ == "__main__":
    unittest.main()
