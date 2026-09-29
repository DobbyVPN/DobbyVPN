from __future__ import annotations

import unittest

from torturer_checks.hosted.cli import CommandResult
from torturer_checks.hosted.linux import LinuxHostedAdapter


class LinuxPreflightTests(unittest.TestCase):
    def test_hosted_linux_discovers_uplink_for_routing_proof(self) -> None:
        commands: list[tuple[str, ...]] = []

        class Runner:
            def run(self, command, *, timeout_seconds):
                self.asserted_timeout = timeout_seconds
                arguments = tuple(command)
                commands.append(arguments)
                if arguments[0] == "/usr/bin/getent":
                    return CommandResult(arguments, 0, b"104.26.12.205 STREAM api.ipify.org\n")
                if arguments[0] == "/usr/sbin/ip":
                    return CommandResult(arguments, 0, b"104.26.12.205 via 10.1.0.1 dev eth0 src 10.1.0.221\n")
                raise AssertionError(arguments)

        adapter = object.__new__(LinuxHostedAdapter)
        adapter.runner = Runner()
        adapter.identity_url = "https://api.ipify.org"
        adapter.network_interface = None
        adapter._resolve_routing_probe(5.0)
        self.assertEqual(adapter.network_interface, "eth0")
        self.assertEqual(
            commands[-1],
            ("/usr/sbin/ip", "-o", "route", "get", "104.26.12.205"),
        )


if __name__ == "__main__":
    unittest.main()
