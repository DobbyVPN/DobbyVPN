"""Tests for the direct Windows routing oracle used by functional probes."""

from __future__ import annotations

import ctypes
import sys
import unittest
from unittest import mock

from torturer_checks.windows_route import best_ipv4_interface, main


class WindowsRouteTests(unittest.TestCase):
    def test_best_ipv4_interface_uses_ip_helper_with_network_bytes(self) -> None:
        calls: list[int] = []

        def lookup(destination: int, output: object) -> int:
            calls.append(destination)
            ctypes.cast(output, ctypes.POINTER(ctypes.c_uint32)).contents.value = 17
            return 0

        api = mock.Mock(GetBestInterface=lookup)
        with mock.patch.object(ctypes, "WinDLL", return_value=api, create=True):
            self.assertEqual(best_ipv4_interface("1.2.3.4"), 17)
        self.assertEqual(calls, [int.from_bytes(b"\x01\x02\x03\x04", sys.byteorder)])

    def test_best_ipv4_interface_rejects_errors(self) -> None:
        def lookup(_destination: int, _output: object) -> int:
            return 123

        api = mock.Mock(GetBestInterface=lookup)
        with mock.patch.object(ctypes, "WinDLL", return_value=api, create=True):
            with self.assertRaisesRegex(OSError, "GetBestInterface failed"):
                best_ipv4_interface("1.2.3.4")

    def test_invalid_address_does_not_call_windows_api(self) -> None:
        with mock.patch.object(ctypes, "WinDLL", create=True) as api:
            self.assertEqual(main(["not-an-address"]), 1)
        api.assert_not_called()


if __name__ == "__main__":
    unittest.main()
