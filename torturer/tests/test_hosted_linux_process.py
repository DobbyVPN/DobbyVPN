from __future__ import annotations

import unittest

from torturer_checks.hosted.cli import CommandResult
from torturer_checks.hosted.linux import _process_stat_is_absent


class LinuxProcessStatTests(unittest.TestCase):
    def test_process_exit_between_proc_check_and_open_is_absence(self) -> None:
        result = CommandResult(
            command=("cat", "/proc/2513/stat"),
            returncode=2,
            stdout=b"service_probe_absent\n",
            stderr=b"cat: /proc/2513/stat: No such file or directory\n",
        )

        self.assertTrue(_process_stat_is_absent(result, 2513))

    def test_initial_proc_miss_is_absence(self) -> None:
        result = CommandResult(
            command=("sh", "-c", "test -e /proc/2513/stat"),
            returncode=2,
            stdout=b"service_probe_absent\n",
        )

        self.assertTrue(_process_stat_is_absent(result, 2513))

    def test_unexpected_probe_diagnostics_are_not_absence(self) -> None:
        results = (
            CommandResult(
                command=("cat", "/proc/2513/stat"),
                returncode=2,
                stdout=b"service_probe_absent\n",
                stderr=b"cat: /proc/2513/stat: Permission denied\n",
            ),
            CommandResult(
                command=("cat", "/proc/2513/stat"),
                returncode=2,
                stdout=b"service_probe_absent\n",
                stderr=b"cat: /proc/9999/stat: No such file or directory\n",
            ),
            CommandResult(
                command=("cat", "/proc/2513/stat"),
                returncode=1,
                stdout=b"service_probe_absent\n",
            ),
            CommandResult(
                command=("cat", "/proc/2513/stat"),
                returncode=2,
                stdout=b"service_probe_error\n",
            ),
        )

        for result in results:
            with self.subTest(result=result):
                self.assertFalse(_process_stat_is_absent(result, 2513))


if __name__ == "__main__":
    unittest.main()
