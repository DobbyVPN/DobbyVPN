from __future__ import annotations

import json
import subprocess
import unittest
from unittest import mock

import windows_process_census as census


class WindowsProcessCensusTests(unittest.TestCase):
    def test_returns_complete_active_set_and_transitive_descendants(self) -> None:
        records = [
            {"ProcessId": 0, "ParentProcessId": 0},
            {"ProcessId": 100, "ParentProcessId": 1},
            {"ProcessId": 200, "ParentProcessId": 100},
            {"ProcessId": 300, "ParentProcessId": 200},
            {"ProcessId": 400, "ParentProcessId": 1},
        ]
        completed = subprocess.CompletedProcess(
            ["powershell.exe"],
            0,
            stdout=json.dumps(records).encode("utf-8"),
            stderr=b"",
        )
        with mock.patch.object(census.subprocess, "run", return_value=completed) as run:
            descendants, active_pids = census.windows_process_census(
                100,
                timeout_seconds=7,
            )

        self.assertEqual(descendants, {200, 300})
        self.assertEqual(active_pids, {0, 100, 200, 300, 400})
        self.assertEqual(run.call_args.kwargs["timeout"], 7)

    def test_nonempty_census_proves_unlisted_process_absent(self) -> None:
        completed = subprocess.CompletedProcess(
            ["powershell.exe"],
            0,
            stdout=b'{"ProcessId":456,"ParentProcessId":1}',
            stderr=b"",
        )
        with mock.patch.object(census.subprocess, "run", return_value=completed):
            descendants, active_pids = census.windows_process_census(
                123,
                timeout_seconds=2,
            )
        self.assertEqual(descendants, set())
        self.assertEqual(active_pids, {456})

    def test_empty_output_fails_closed(self) -> None:
        completed = subprocess.CompletedProcess(
            ["powershell.exe"], 0, stdout=b"", stderr=b""
        )
        with (
            mock.patch.object(census.subprocess, "run", return_value=completed),
            self.assertRaisesRegex(
                census.WindowsProcessCensusError,
                "returned empty output",
            ),
        ):
            census.windows_process_census(123, timeout_seconds=2)

    def test_malformed_record_fails_closed(self) -> None:
        completed = subprocess.CompletedProcess(
            ["powershell.exe"],
            0,
            stdout=b'[{"ProcessId":123}]',
            stderr=b"",
        )
        with (
            mock.patch.object(census.subprocess, "run", return_value=completed),
            self.assertRaisesRegex(
                census.WindowsProcessCensusError,
                "invalid process record",
            ),
        ):
            census.windows_process_census(123, timeout_seconds=2)

    def test_query_error_fails_closed_with_complete_diagnostic(self) -> None:
        completed = subprocess.CompletedProcess(
            ["powershell.exe"],
            9,
            stdout=b"query stdout",
            stderr=b"query stderr",
        )
        with (
            mock.patch.object(census.subprocess, "run", return_value=completed),
            self.assertRaisesRegex(
                census.WindowsProcessCensusError,
                "exit=9 stdout=query stdout stderr=query stderr",
            ),
        ):
            census.windows_process_census(123, timeout_seconds=2)

    def test_timeout_fails_closed_with_complete_stream_diagnostic(self) -> None:
        timeout = subprocess.TimeoutExpired(
            ["powershell.exe"],
            2,
            output=b"partial stdout",
            stderr=b"partial stderr",
        )
        with (
            mock.patch.object(census.subprocess, "run", side_effect=timeout),
            self.assertRaisesRegex(
                census.WindowsProcessCensusError,
                "timed out after 2s stdout=partial stdout stderr=partial stderr",
            ),
        ):
            census.windows_process_census(123, timeout_seconds=2)

    def test_missing_powershell_fails_closed(self) -> None:
        with (
            mock.patch.object(
                census.subprocess,
                "run",
                side_effect=FileNotFoundError("powershell.exe not found"),
            ),
            self.assertRaisesRegex(
                census.WindowsProcessCensusError,
                "could not start: powershell.exe not found",
            ),
        ):
            census.windows_process_census(123, timeout_seconds=2)


if __name__ == "__main__":
    unittest.main()
