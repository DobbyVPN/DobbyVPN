"""Tests for the direct Windows routing oracle used by functional probes."""

from __future__ import annotations

import ctypes
import io
import json
import sys
import unittest
from collections.abc import Sequence
from contextlib import redirect_stdout
from unittest import mock

from torturer_contract.functional.engine import ScenarioExecutionError
from torturer_checks.hosted.cli import CommandResult, HostedAdapterError
from torturer_checks.hosted.windows import WindowsHostedAdapter
from torturer_checks.windows_route import (
    _MibIfRow2,
    best_ipv4_interface,
    interface_counters,
    main,
)


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

    def test_mib_if_row2_matches_windows_counter_offsets(self) -> None:
        self.assertEqual(_MibIfRow2.InterfaceIndex.offset, 8)
        self.assertEqual(_MibIfRow2.InOctets.offset, 1208)
        self.assertEqual(_MibIfRow2.OutOctets.offset, 1280)

    def test_interface_counters_query_the_requested_index(self) -> None:
        indexes: list[int] = []

        def lookup(pointer: object) -> int:
            row = ctypes.cast(pointer, ctypes.POINTER(_MibIfRow2)).contents
            indexes.append(row.InterfaceIndex)
            row.InOctets = 0x1_0000_0002
            row.OutOctets = 0x2_0000_0003
            return 0

        api = mock.Mock(GetIfEntry2=lookup)
        with mock.patch.object(ctypes, "WinDLL", return_value=api, create=True):
            self.assertEqual(interface_counters(37), (37, 0x1_0000_0002, 0x2_0000_0003))
        self.assertEqual(indexes, [37])

    def test_counter_cli_returns_interface_and_byte_counts_as_json(self) -> None:
        def lookup(pointer: object) -> int:
            row = ctypes.cast(pointer, ctypes.POINTER(_MibIfRow2)).contents
            row.InOctets = 1234
            row.OutOctets = 5678
            return 0

        api = mock.Mock(GetIfEntry2=lookup)
        output = io.StringIO()
        with (
            mock.patch.object(ctypes, "WinDLL", return_value=api, create=True),
            redirect_stdout(output),
        ):
            self.assertEqual(main(["--counters", "37"]), 0)
        self.assertEqual(
            json.loads(output.getvalue()),
            {"interface_index": 37, "received_bytes": 1234, "sent_bytes": 5678},
        )

    def test_hosted_adapter_runs_counter_helper_with_the_scenario_timeout(self) -> None:
        class Runner:
            def __init__(self) -> None:
                self.command: tuple[str, ...] | None = None
                self.timeout: float | None = None

            def run(self, command: Sequence[str], *, timeout_seconds: float) -> CommandResult:
                command_tuple = tuple(command)
                self.command = command_tuple
                self.timeout = timeout_seconds
                return CommandResult(
                    command_tuple,
                    0,
                    b'{"interface_index":37,"received_bytes":1234,"sent_bytes":5678}',
                )

        runner = Runner()
        adapter = object.__new__(WindowsHostedAdapter)
        adapter.runner = runner

        self.assertEqual(adapter._interface_counters("37", 1.25), (1234, 5678))
        self.assertEqual(
            runner.command,
            (
                sys.executable,
                "-m",
                "torturer_checks.windows_route",
                "--counters",
                "37",
            ),
        )
        self.assertEqual(runner.timeout, 1.25)

    def test_hosted_adapter_preserves_runner_timeout_streams(self) -> None:
        class Runner:
            def run(self, _command: Sequence[str], *, timeout_seconds: float) -> CommandResult:
                error = HostedAdapterError(
                    "COMMAND_TIMEOUT", stdout=b"complete stdout", stderr=b"complete stderr"
                )
                raise error

        adapter = object.__new__(WindowsHostedAdapter)
        adapter.runner = Runner()

        with self.assertRaises(ScenarioExecutionError) as caught:
            adapter._interface_counters("37", 1.25)

        self.assertEqual(caught.exception.reason_code, "COMMAND_TIMEOUT")
        notes = "\n".join(caught.exception.__notes__)
        self.assertIn("complete stdout", notes)
        self.assertIn("complete stderr", notes)


if __name__ == "__main__":
    unittest.main()
